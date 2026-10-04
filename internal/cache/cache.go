package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const statusHeader = "X-Hamsterd-Cache"

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
	mu             sync.Mutex
	refreshing     map[string]struct{}
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
	if err := os.Chmod(config.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure cache directory: %w", err)
	}
	return &Transport{
		base: base,
		store: &store{
			directory:      config.Directory,
			maxBytes:       config.MaxBytes,
			maxObjectBytes: config.MaxObjectBytes,
			defaultTTL:     config.DefaultTTL,
			refreshing:     make(map[string]struct{}),
		},
	}, nil
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
	if response, stale, ok := t.store.load(key, request); ok {
		if stale && t.store.beginRefresh(key) {
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
	if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Range") != "" {
		return false
	}
	directives := parseCacheControl(request.Header.Get("Cache-Control"))
	_, noStore := directives["no-store"]
	_, noCache := directives["no-cache"]
	return !noStore && !noCache
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
	} else {
		freshUntil = now.Add(defaultTTL)
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
	metaPath, bodyPath := s.paths(key)
	metaFile, err := os.Open(metaPath)
	if err != nil {
		return nil, false, false
	}
	var meta metadata
	err = json.NewDecoder(io.LimitReader(metaFile, 1<<20)).Decode(&meta)
	_ = metaFile.Close()
	staleUntil := meta.StaleUntil
	if staleUntil.IsZero() {
		staleUntil = meta.ExpiresAt
	}
	now := time.Now()
	if err != nil || now.After(staleUntil) {
		s.remove(key)
		return nil, false, false
	}
	body, err := os.Open(bodyPath)
	if err != nil {
		s.remove(key)
		return nil, false, false
	}
	info, err := body.Stat()
	if err != nil || info.Size() != meta.Size {
		_ = body.Close()
		s.remove(key)
		return nil, false, false
	}
	_ = os.Chtimes(metaPath, now, now)
	_ = os.Chtimes(bodyPath, now, now)
	header := meta.Header.Clone()
	stale := now.After(meta.ExpiresAt)
	if stale {
		header.Set(statusHeader, "STALE")
	} else {
		header.Set(statusHeader, "HIT")
	}
	originAge, _ := responseAge(&http.Response{Header: meta.Header})
	header.Set("Age", strconv.FormatInt(max(0, originAge+int64(now.Sub(meta.StoredAt)/time.Second)), 10))
	return &http.Response{
		Status:        meta.Status,
		StatusCode:    meta.StatusCode,
		Proto:         meta.Proto,
		ProtoMajor:    meta.ProtoMajor,
		ProtoMinor:    meta.ProtoMinor,
		Header:        header,
		Body:          body,
		ContentLength: meta.Size,
		Request:       request,
	}, stale, true
}

func (s *store) beginRefresh(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.refreshing[key]; ok {
		return false
	}
	s.refreshing[key] = struct{}{}
	return true
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
		} else if _, err := body.temporary.Write(buffer[:n]); err != nil {
			body.disable()
		} else {
			body.written += int64(n)
		}
	}
	if readErr == io.EOF && !body.disabled {
		body.meta.Size = body.written
		if err := body.store.commit(body.key, body.temporary, body.meta); err == nil {
			body.committed = true
		} else {
			body.disable()
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
	name := body.temporary.Name()
	_ = body.temporary.Close()
	_ = os.Remove(name)
}

func (s *store) commit(key string, temporary *os.File, meta metadata) error {
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	metaPath, bodyPath := s.paths(key)
	if err := replace(temporary.Name(), bodyPath); err != nil {
		return err
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		_ = os.Remove(bodyPath)
		return err
	}
	metaTemp, err := os.CreateTemp(s.directory, ".metadata-*.tmp")
	if err != nil {
		_ = os.Remove(bodyPath)
		return err
	}
	metaTempName := metaTemp.Name()
	defer os.Remove(metaTempName)
	if err := metaTemp.Chmod(0o600); err != nil {
		metaTemp.Close()
		_ = os.Remove(bodyPath)
		return err
	}
	if _, err := metaTemp.Write(metaBytes); err != nil {
		metaTemp.Close()
		_ = os.Remove(bodyPath)
		return err
	}
	if err := metaTemp.Close(); err != nil {
		_ = os.Remove(bodyPath)
		return err
	}
	if err := replace(metaTempName, metaPath); err != nil {
		_ = os.Remove(bodyPath)
		return err
	}
	s.evict()
	return nil
}

func replace(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, destination)
}

func (s *store) remove(key string) {
	metaPath, bodyPath := s.paths(key)
	_ = os.Remove(metaPath)
	_ = os.Remove(bodyPath)
}

func (s *store) evict() {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return
	}
	type entry struct {
		key     string
		size    int64
		modTime time.Time
	}
	var bodies []entry
	var total int64
	for _, item := range entries {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".body") {
			continue
		}
		info, err := item.Info()
		if err != nil {
			continue
		}
		bodies = append(bodies, entry{
			key:     strings.TrimSuffix(item.Name(), ".body"),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		total += info.Size()
	}
	sort.Slice(bodies, func(i, j int) bool { return bodies[i].modTime.Before(bodies[j].modTime) })
	for _, item := range bodies {
		if total <= s.maxBytes {
			break
		}
		s.remove(item.key)
		total -= item.size
	}
}
