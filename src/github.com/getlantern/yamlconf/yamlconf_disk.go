package yamlconf

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"reflect"

	"github.com/getlantern/yaml"
)

func (m *Manager) loadFromDisk() error {
	_, err := m.reloadFromDisk()
	return err
}

func (m *Manager) reloadFromDisk() (bool, error) {
	fileInfo, err := os.Stat(m.FilePath)
	if err != nil {
		return false, fmt.Errorf("Unable to stat config file %s: %s", m.FilePath, err)
	}
	if m.fileInfo == fileInfo {
		log.Trace("Config unchanged on disk")
		return false, nil
	}

	var cfg Config
	if m.ObfuscationKey != nil {
		log.Trace("Attempting to read obfuscated config")
		var err1, err2 error
		cfg, err1 = m.doReadFromDisk(true)
		if err1 != nil {
			log.Tracef("Error reading obfuscated config from disk, try reading non-obfuscated: %v", err)
			cfg, err2 = m.doReadFromDisk(false)
			if err2 != nil {
				return false, err1
			}
		}
	} else {
		log.Trace("Attempting to read non-obfuscated config")
		cfg, err = m.doReadFromDisk(false)
		if err != nil {
			return false, err
		}
	}

	if m.cfg != nil && m.cfg.GetVersion() != cfg.GetVersion() {
		log.Trace("Version mismatch on disk, overwriting what's on disk with current version")
		if err := m.writeToDisk(m.cfg); err != nil {
			log.Errorf("Unable to write to disk: %v", err)
		}
		return false, fmt.Errorf("Version of config on disk did not match expected. Expected %d, found %d", m.cfg.GetVersion(), cfg.GetVersion())
	}

	if reflect.DeepEqual(m.cfg, cfg) {
		log.Trace("Config on disk is same as in memory, ignoring")
		return false, nil
	}

	log.Debugf("Configuration changed on disk, applying")

	m.setCfg(cfg)
	m.fileInfo = fileInfo

	return true, nil
}

func (m *Manager) doReadFromDisk(allowObfuscation bool) (Config, error) {
	infile, err := os.Open(m.FilePath)
	if err != nil {
		return nil, fmt.Errorf("Unable to open config file %v for reading: %v", m.FilePath, err)
	}
	defer infile.Close()

	var in io.Reader = infile
	if allowObfuscation && m.ObfuscationKey != nil {
		// Read file as obfuscated with AES
		stream, err := m.obfuscationStream()
		if err != nil {
			return nil, err
		}
		in = &cipher.StreamReader{S: stream, R: in}
	}

	bytes, err := ioutil.ReadAll(in)
	if err != nil {
		return nil, fmt.Errorf("Error reading config from %s: %s", m.FilePath, err)
	}

	cfg := m.EmptyConfig()
	err = yaml.Unmarshal(bytes, cfg)
	if err != nil {
		return nil, fmt.Errorf("Error unmarshaling config yaml from %s: %s", m.FilePath, err)
	}

	return cfg, nil
}

func (m *Manager) saveToDiskAndUpdate(updated Config) (bool, error) {
	log.Trace("Applying defaults before saving")
	updated.ApplyDefaults()

	log.Trace("Remembering current version")
	original := m.cfg
	nextVersion := 0
	if original != nil {
		log.Trace("Copying original config in preparation for comparison")
		var err error
		original, err = m.copy(m.cfg)
		if err != nil {
			return false, fmt.Errorf("Unable to copy original config for comparison")
		}
		log.Trace("Set version to 0 prior to comparison")
		original.SetVersion(0)
		log.Trace("Incrementing version")
		nextVersion = m.cfg.GetVersion() + 1
	}

	log.Trace("Compare config without version")
	updated.SetVersion(0)
	if reflect.DeepEqual(original, updated) {
		log.Trace("Configuration unchanged, do nothing")
		return false, nil
	}

	log.Debug("Configuration changed programmatically, saving")
	log.Trace("Increment version")
	updated.SetVersion(nextVersion)

	log.Trace("Save updated")
	err := m.writeToDisk(updated)
	if err != nil {
		return false, fmt.Errorf("Unable to write to disk: %v", err)
	}

	log.Trace("Point to updated")
	m.setCfg(updated)
	return true, nil
}

func (m *Manager) writeToDisk(cfg Config) error {
	bytes, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("Unable to marshal config yaml: %s", err)
	}

	outfile, err := os.OpenFile(m.FilePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("Unable to open file %v for writing: %v", m.FilePath, err)
	}
	defer outfile.Close()

	var out io.Writer = outfile
	if m.ObfuscationKey != nil {
		// write file as obfuscated with AES
		stream, err := m.obfuscationStream()
		if err != nil {
			return err
		}
		out = &cipher.StreamWriter{S: stream, W: out}
	}
	_, err = out.Write(bytes)
	if err != nil {
		return fmt.Errorf("Unable to write config yaml to file %s: %s", m.FilePath, err)
	}
	m.fileInfo, err = os.Stat(m.FilePath)
	if err != nil {
		return fmt.Errorf("Unable to stat file %s: %s", m.FilePath, err)
	}
	return nil
}

// HasChangedOnDisk checks whether Config has changed on disk
func (m *Manager) hasChangedOnDisk() bool {
	nextFileInfo, err := os.Stat(m.fileInfo.Name())
	if err != nil {
		return false
	}
	hasChanged := nextFileInfo.Size() != m.fileInfo.Size() || nextFileInfo.ModTime() != m.fileInfo.ModTime()
	return hasChanged
}

func (m *Manager) obfuscationStream() (cipher.Stream, error) {
	block, err := aes.NewCipher(m.ObfuscationKey)
	if err != nil {
		return nil, fmt.Errorf("Unable to initialize AES for obfuscation: %v", err)
	}
	iv := make([]byte, block.BlockSize())
	return cipher.NewOFB(block, iv), nil
}
