package cache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	statusHeader = "X-Hamsterd-Cache"
	// maxMetadataBytes bounds the stored response headers of one entry.
	maxMetadataBytes = 64 << 10
	// staleTemporaryAge is how long an unmodified temporary file may remain
	// before startup treats it as left over from an interrupted process.
	staleTemporaryAge = time.Hour
)

var (
	entryName     = regexp.MustCompile(`^([0-9a-f]{64})\.(body|json)$`)
	temporaryName = regexp.MustCompile(`^\.(cache|metadata)-[0-9]+\.tmp$`)
)

type Config struct {
	Directory      string
	MaxBytes       int64
	MaxObjectBytes int64
	DefaultTTL     time.Duration
}

type Transport struct {
	base  http.RoundTripper
	store *store
}

type store struct {
	directory      string
	maxBytes       int64
	maxObjectBytes int64
	defaultTTL     time.Duration
	// pending counts bytes in temporary files of downloads in progress.
	pending atomic.Int64

	mu         sync.Mutex
	refreshing map[string]struct{}
	// index tracks committed entries by recency; front is most recent.
	index   *list.List
	entries map[string]*list.Element
	total   int64
}

type indexEntry struct {
	key  string
	size int64
}

type metadata struct {
	Status     string      `json:"status"`
	StatusCode int         `json:"status_code"`
	Proto      string      `json:"proto"`
	ProtoMajor int         `json:"proto_major"`
	ProtoMinor int         `json:"proto_minor"`
	Header     http.Header `json:"header"`
	Size       int64       `json:"size"`
	StoredAt   time.Time   `json:"stored_at"`
	ExpiresAt  time.Time   `json:"expires_at"`
	StaleUntil time.Time   `json:"stale_until,omitempty"`
}

func NewTransport(base http.RoundTripper, config Config) (*Transport, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	if config.Directory == "" {
		return nil, fmt.Errorf("cache directory must not be empty")
	}
	if config.MaxBytes <= 0 || config.MaxObjectBytes <= 0 || config.MaxObjectBytes > config.MaxBytes {
		return nil, fmt.Errorf("invalid cache size limits")
	}
	if config.DefaultTTL <= 0 {
		return nil, fmt.Errorf("default cache TTL must be positive")
	}
	if err := os.MkdirAll(config.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("create cache directory: %w", err)
	}
	s := &store{
		directory:      config.Directory,
		maxBytes:       config.MaxBytes,
		maxObjectBytes: config.MaxObjectBytes,
		defaultTTL:     config.DefaultTTL,
		refreshing:     make(map[string]struct{}),
		index:          list.New(),
		entries:        make(map[string]*list.Element),
	}
	// Scan before chmod so that a mistakenly shared directory is rejected
	// without changing its permissions.
	if err := s.scan(time.Now()); err != nil {
		return nil, err
	}
	if err := os.Chmod(config.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure cache directory: %w", err)
	}
	return &Transport{base: base, store: s}, nil
}

// scan rebuilds the in-memory index from the cache directory. It refuses a
// directory that contains files hamsterd did not create, because eviction
// deletes entries and the directory is made private.
func (s *store) scan(now time.Time) error {
	items, err := os.ReadDir(s.directory)
	if err != nil {
		return fmt.Errorf("read cache directory: %w", err)
	}
	type scanned struct {
		size    int64
		modTime time.Time
		parts   int
	}
	found := make(map[string]*scanned)
	for _, item := range items {
		name := item.Name()
		if name == ".DS_Store" {
			continue
		}
		if temporaryName.MatchString(name) {
			if info, err := item.Info(); err == nil && now.Sub(info.ModTime()) > staleTemporaryAge {
				_ = os.Remove(filepath.Join(s.directory, name))
			}
			continue
		}
		match := entryName.FindStringSubmatch(name)
		if match == nil || !item.Type().IsRegular() {
			return fmt.Errorf("cache directory %q contains %q, which hamsterd did not create; use a dedicated cache directory", s.directory, name)
		}
		info, err := item.Info()
		if err != nil {
			continue
		}
		entry := found[match[1]]
		if entry == nil {
			entry = &scanned{}
			found[match[1]] = entry
		}
		entry.size += info.Size()
		entry.parts++
		if info.ModTime().After(entry.modTime) {
			entry.modTime = info.ModTime()
		}
	}
	keys := make([]string, 0, len(found))
	for key, entry := range found {
		if entry.parts != 2 {
			s.removeLocked(key)
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return found[keys[i]].modTime.Before(found[keys[j]].modTime) })
	for _, key := range keys {
		s.entries[key] = s.index.PushFront(&indexEntry{key: key, size: found[key].size})
		s.total += found[key].size
	}
	s.evictLocked()
	return nil
}

func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !cacheableRequest(request) {
		response, err := t.base.RoundTrip(request)
		if response != nil {
			response.Header.Set(statusHeader, "BYPASS")
		}
		return response, err
	}
	key := cacheKey(request)
	if response, refresh, ok := t.store.load(key, request); ok {
		if refresh {
			go t.refresh(key, request)
		}
		return response, nil
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	expiration, ok := responseExpiration(response, time.Now(), t.store.defaultTTL)
	if !ok || response.Body == nil || response.ContentLength > t.store.maxObjectBytes {
		response.Header.Set(statusHeader, "BYPASS")
		return response, nil
	}
	response.Header.Set(statusHeader, "MISS")
	meta := metadata{
		Status:     response.Status,
		StatusCode: response.StatusCode,
		Proto:      response.Proto,
		ProtoMajor: response.ProtoMajor,
		ProtoMinor: response.ProtoMinor,
		Header:     response.Header.Clone(),
		StoredAt:   time.Now(),
		ExpiresAt:  expiration.freshUntil,
		StaleUntil: expiration.staleUntil,
	}
	meta.Header.Del(statusHeader)
	body, err := t.store.capture(key, response.Body, meta)
	if err != nil {
		response.Header.Set(statusHeader, "BYPASS")
		return response, nil
	}
	response.Body = body
	return response, nil
}

func (t *Transport) refresh(key string, request *http.Request) {
	defer t.store.endRefresh(key)
	request = request.Clone(context.Background())
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return
	}
	expiration, ok := responseExpiration(response, time.Now(), t.store.defaultTTL)
	if !ok || response.Body == nil || response.ContentLength > t.store.maxObjectBytes {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		return
	}
	meta := metadata{
		Status:     response.Status,
		StatusCode: response.StatusCode,
		Proto:      response.Proto,
		ProtoMajor: response.ProtoMajor,
		ProtoMinor: response.ProtoMinor,
		Header:     response.Header.Clone(),
		StoredAt:   time.Now(),
		ExpiresAt:  expiration.freshUntil,
		StaleUntil: expiration.staleUntil,
	}
	body, err := t.store.capture(key, response.Body, meta)
	if err != nil {
		_ = response.Body.Close()
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

func cacheableRequest(request *http.Request) bool {
	if request.Method != http.MethodGet || request.URL == nil || request.URL.User != nil {
		return false
	}
	if request.Header.Get("Range") != "" || hasCredentialHeader(request.Header) || privateDestination(request.URL.Hostname()) {
		return false
	}
	directives := parseCacheControl(request.Header.Get("Cache-Control"))
	_, noStore := directives["no-store"]
	_, noCache := directives["no-cache"]
	return !noStore && !noCache
}

var credentialHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"metadata":            {}, // Azure instance metadata
	"metadata-flavor":     {}, // Google Cloud metadata
	"x-jfrog-art-api":     {},
}

var credentialHeaderParts = []string{"auth", "token", "api-key", "apikey", "api_key", "secret", "password", "credential", "session", "signature"}

// hasCredentialHeader reports request headers that commonly carry
// credentials. A response to such a request may be specific to the caller,
// so it must not be served to other clients.
func hasCredentialHeader(header http.Header) bool {
	for name := range header {
		lower := strings.ToLower(name)
		if _, ok := credentialHeaders[lower]; ok {
			return true
		}
		for _, part := range credentialHeaderParts {
			if strings.Contains(lower, part) {
				return true
			}
		}
	}
	return false
}

// privateDestination reports link-local and cloud metadata hosts, whose
// responses describe the proxy host, often with credentials, rather than a
// public artifact. Loopback origins stay cacheable for local testing; remote
// clients cannot reach them through the destination guard.
func privateDestination(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "metadata.google.internal" || host == "metadata.goog" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLinkLocalUnicast() || ip.Equal(net.ParseIP("fd00:ec2::254")) || ip.Equal(net.ParseIP("100.100.100.200")))
}

type expiration struct {
	freshUntil time.Time
	staleUntil time.Time
}

func responseExpiration(response *http.Response, now time.Time, defaultTTL time.Duration) (expiration, bool) {
	if response.StatusCode != http.StatusOK || response.Header.Get("Set-Cookie") != "" || !supportedVary(response.Header.Values("Vary")) || response.Header.Get("Content-Range") != "" {
		return expiration{}, false
	}
	directives := parseCacheControl(response.Header.Get("Cache-Control"))
	_, noStore := directives["no-store"]
	_, noCache := directives["no-cache"]
	_, private := directives["private"]
	if noStore || noCache || private {
		return expiration{}, false
	}
	var freshUntil time.Time
	if raw, ok := directives.value("max-age"); ok {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || seconds < 0 {
			return expiration{}, false
		}
		age, err := responseAge(response)
		if err != nil {
			return expiration{}, false
		}
		freshUntil = now.Add(time.Duration(seconds-age) * time.Second)
	} else if raw := response.Header.Get("Expires"); raw != "" {
		expiresAt, err := http.ParseTime(raw)
		if err != nil || !expiresAt.After(now) {
			return expiration{}, false
		}
		freshUntil = expiresAt
	} else if response.Header.Get("ETag") != "" || response.Header.Get("Last-Modified") != "" {
		// Without explicit freshness, only artifact-like responses that
		// carry a validator receive the default TTL.
		freshUntil = now.Add(defaultTTL)
	} else {
		return expiration{}, false
	}
	staleUntil := freshUntil
	if raw, ok := directives.value("stale-while-revalidate"); ok {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || seconds < 0 {
			return expiration{}, false
		}
		staleUntil = staleUntil.Add(time.Duration(seconds) * time.Second)
	}
	result := expiration{freshUntil: freshUntil, staleUntil: staleUntil}
	return result, staleUntil.After(now)
}

func responseAge(response *http.Response) (int64, error) {
	raw := response.Header.Get("Age")
	if raw == "" {
		return 0, nil
	}
	age, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || age < 0 {
		return 0, fmt.Errorf("invalid Age header %q", raw)
	}
	return age, nil
}

func supportedVary(values []string) bool {
	for _, value := range values {
		for _, field := range strings.Split(value, ",") {
			switch strings.ToLower(strings.TrimSpace(field)) {
			case "", "accept-encoding", "origin":
			default:
				return false
			}
		}
	}
	return true
}

type directives map[string]string

func (d directives) value(key string) (string, bool) {
	value, ok := d[key]
	return value, ok
}

func parseCacheControl(value string) directives {
	result := directives{}
	for _, part := range strings.Split(value, ",") {
		key, raw, found := strings.Cut(strings.TrimSpace(part), "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		if found {
			result[key] = strings.Trim(strings.TrimSpace(raw), `"`)
		} else {
			result[key] = ""
		}
	}
	return result
}

func cacheKey(request *http.Request) string {
	key := request.Method + "\n" + request.URL.String() +
		"\naccept-encoding:" + strings.Join(request.Header.Values("Accept-Encoding"), "\x00") +
		"\norigin:" + strings.Join(request.Header.Values("Origin"), "\x00")
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (s *store) paths(key string) (string, string) {
	return filepath.Join(s.directory, key+".json"), filepath.Join(s.directory, key+".body")
}

func (s *store) load(key string, request *http.Request) (*http.Response, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	metaPath, bodyPath := s.paths(key)
	metaFile, err := os.Open(metaPath)
	if err != nil {
		return nil, false, false
	}
	var meta metadata
	err = json.NewDecoder(io.LimitReader(metaFile, maxMetadataBytes)).Decode(&meta)
	_ = metaFile.Close()
	staleUntil := meta.StaleUntil
	if staleUntil.IsZero() {
		staleUntil = meta.ExpiresAt
	}
	now := time.Now()
	if err != nil || now.After(staleUntil) {
		s.removeLocked(key)
		return nil, false, false
	}
	body, err := os.Open(bodyPath)
	if err != nil {
		s.removeLocked(key)
		return nil, false, false
	}
	info, err := body.Stat()
	if err != nil || info.Size() != meta.Size {
		_ = body.Close()
		s.removeLocked(key)
		return nil, false, false
	}
	// File times persist recency across restarts; the index keeps it in memory.
	_ = os.Chtimes(metaPath, now, now)
	_ = os.Chtimes(bodyPath, now, now)
	if element := s.entries[key]; element != nil {
		s.index.MoveToFront(element)
	}
	header := meta.Header.Clone()
	stale := now.After(meta.ExpiresAt)
	if stale {
		header.Set(statusHeader, "STALE")
	} else {
		header.Set(statusHeader, "HIT")
	}
	originAge, _ := responseAge(&http.Response{Header: meta.Header})
	header.Set("Age", strconv.FormatInt(max(0, originAge+int64(now.Sub(meta.StoredAt)/time.Second)), 10))
	response := &http.Response{
		Status:        meta.Status,
		StatusCode:    meta.StatusCode,
		Proto:         meta.Proto,
		ProtoMajor:    meta.ProtoMajor,
		ProtoMinor:    meta.ProtoMinor,
		Header:        header,
		Body:          body,
		ContentLength: meta.Size,
		Request:       request,
	}
	refresh := false
	if stale {
		if _, refreshing := s.refreshing[key]; !refreshing {
			s.refreshing[key] = struct{}{}
			refresh = true
		}
	}
	return response, refresh, true
}

func (s *store) endRefresh(key string) {
	s.mu.Lock()
	delete(s.refreshing, key)
	s.mu.Unlock()
}

func (s *store) capture(key string, source io.ReadCloser, meta metadata) (io.ReadCloser, error) {
	temporary, err := os.CreateTemp(s.directory, ".cache-*.tmp")
	if err != nil {
		return nil, err
	}
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		_ = os.Remove(temporary.Name())
		return nil, err
	}
	return &capturingBody{source: source, temporary: temporary, store: s, key: key, meta: meta}, nil
}

type capturingBody struct {
	source    io.ReadCloser
	temporary *os.File
	store     *store
	key       string
	meta      metadata
	written   int64
	disabled  bool
	committed bool
}

func (body *capturingBody) Read(buffer []byte) (int, error) {
	n, readErr := body.source.Read(buffer)
	if n > 0 && !body.disabled {
		if body.written+int64(n) > body.store.maxObjectBytes {
			body.disable()
		} else if body.store.pending.Add(int64(n)) > body.store.maxBytes {
			// Concurrent downloads may not hold more temporary data than
			// the whole cache budget.
			body.store.pending.Add(-int64(n))
			body.disable()
		} else if _, err := body.temporary.Write(buffer[:n]); err != nil {
			body.store.pending.Add(-int64(n))
			body.disable()
		} else {
			body.written += int64(n)
		}
	}
	if readErr == io.EOF && !body.disabled {
		body.meta.Size = body.written
		err := body.store.commit(body.key, body.temporary, body.meta)
		body.store.pending.Add(-body.written)
		body.written = 0
		if err == nil {
			body.committed = true
		} else {
			body.disabled = true
		}
	}
	return n, readErr
}

func (body *capturingBody) Close() error {
	if !body.committed {
		body.disable()
	}
	return body.source.Close()
}

func (body *capturingBody) disable() {
	if body.disabled || body.committed {
		return
	}
	body.disabled = true
	body.store.pending.Add(-body.written)
	body.written = 0
	name := body.temporary.Name()
	_ = body.temporary.Close()
	_ = os.Remove(name)
}

// commit installs a completed body and its metadata. It removes the
// temporary body on failure.
func (s *store) commit(key string, temporary *os.File, meta metadata) error {
	bodyTempName := temporary.Name()
	defer os.Remove(bodyTempName)
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if len(metaBytes) > maxMetadataBytes {
		return fmt.Errorf("cache metadata exceeds %d bytes", maxMetadataBytes)
	}
	metaTemp, err := os.CreateTemp(s.directory, ".metadata-*.tmp")
	if err != nil {
		return err
	}
	metaTempName := metaTemp.Name()
	defer os.Remove(metaTempName)
	if err := metaTemp.Chmod(0o600); err != nil {
		metaTemp.Close()
		return err
	}
	if _, err := metaTemp.Write(metaBytes); err != nil {
		metaTemp.Close()
		return err
	}
	if err := metaTemp.Close(); err != nil {
		return err
	}

	// Install both files under the lock so that load never pairs a new body
	// with old metadata.
	s.mu.Lock()
	defer s.mu.Unlock()
	metaPath, bodyPath := s.paths(key)
	s.removeLocked(key)
	if err := replace(bodyTempName, bodyPath); err != nil {
		return err
	}
	if err := replace(metaTempName, metaPath); err != nil {
		_ = os.Remove(bodyPath)
		return err
	}
	size := meta.Size + int64(len(metaBytes))
	s.entries[key] = s.index.PushFront(&indexEntry{key: key, size: size})
	s.total += size
	s.evictLocked()
	return nil
}

// replace renames source to destination atomically on the same filesystem.
// The fallback (remove + rename) exists for cross-device moves, but the cache
// directory keeps both temp files and final files, so source and destination
// are always on the same filesystem and the first Rename almost always succeeds.
func replace(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, destination)
}

// removeLocked deletes an entry's files and index record. Callers hold s.mu,
// except scan, which runs before the store is shared.
func (s *store) removeLocked(key string) {
	metaPath, bodyPath := s.paths(key)
	_ = os.Remove(metaPath)
	_ = os.Remove(bodyPath)
	if element := s.entries[key]; element != nil {
		s.total -= element.Value.(*indexEntry).size
		s.index.Remove(element)
		delete(s.entries, key)
	}
}

// evictLocked removes least recently used entries until the cache fits.
func (s *store) evictLocked() {
	for s.total > s.maxBytes {
		oldest := s.index.Back()
		if oldest == nil {
			return
		}
		s.removeLocked(oldest.Value.(*indexEntry).key)
	}
}
