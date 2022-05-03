package proxyconfig

import (
	"encoding/json"
	"fmt"
	"github.com/go-shortcut/httpproxy/v2"
	"io/ioutil"
	"log"
	"os"
)

type ProxyUnitConfig struct {
	Socket        string
	FormatVersion string
}
type ProxyServiceConfig struct {
	DefConfigFN string

	PathConfig string
	PathCaCert string
	PathCaKey  string

	unitConfig ProxyUnitConfig
	caCert     []byte
	caKey      []byte
}

func (c *ProxyServiceConfig) GetCaCert() []byte {
	return c.caCert
}
func (c *ProxyServiceConfig) GetCaKey() []byte {
	return c.caKey
}
func (c *ProxyServiceConfig) GetUnitConfig() ProxyUnitConfig {
	return c.unitConfig
}
func (c *ProxyServiceConfig) Validate() error {
	log.Printf("unitConfig: %+v\n", *c)
	return nil
}

func (c *ProxyServiceConfig) InitConfigs() error {
	// check unitConfig file
	if configFile, err := os.Open(c.PathConfig); err == nil {
		// read file to struct
		defer configFile.Close()
		byteValue, _ := ioutil.ReadAll(configFile)
		var result ProxyUnitConfig
		err := json.Unmarshal(byteValue, &result)
		if err != nil {
			return err
		}
		c.unitConfig = result
		if c.unitConfig.Socket == "" {
			c.unitConfig.Socket = DefaultHttpScoket
		}

		fmt.Println(result.FormatVersion)

	} else {
		// create and fill, if not exist
		configFile, err := createFile(c.PathConfig)
		if err != nil {
			return err
		}
		defer configFile.Close()
		configContent, err := EmbeddedFS.ReadFile(c.DefConfigFN)
		if err != nil {
			return err
		}
		_, err = configFile.Write(configContent)
		if err != nil {
			return err
		}

	}
	// check CA key, create if not exist
	if caKeyFile, err := os.Open(c.PathCaKey); err == nil {
		// read file to struct
		defer caKeyFile.Close()
		byteValue, _ := ioutil.ReadAll(caKeyFile)
		c.caKey = byteValue

	} else {
		// create and fill, if not exist
		caKeyFile, err := createFile(c.PathCaKey)
		if err != nil {
			return err
		}
		defer caKeyFile.Close()
		_, err = caKeyFile.Write(httpproxy.DefaultCaKey)
		if err != nil {
			return err
		}
		c.caKey = httpproxy.DefaultCaKey
		// also create CA cert
		caCertFile, err := createFile(c.PathCaCert)
		if err != nil {
			return err
		}
		defer caCertFile.Close()
		_, err = caCertFile.Write(httpproxy.DefaultCaCert)
		if err != nil {
			return err
		}
		c.caCert = httpproxy.DefaultCaCert
	}
	if caCertFile, err := os.Open(c.PathCaCert); err == nil {
		// CA Cert
		defer caCertFile.Close()
		byteValue, _ := ioutil.ReadAll(caCertFile)
		c.caCert = byteValue

	} else {
		return err
	}

	return nil
}
