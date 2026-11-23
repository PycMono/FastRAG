package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	appconfig "github.com/PycMono/FastRAG/infrastructure/config"
)

const Masked = "••••••••"

var ErrConflict = errors.New("配置已被其他操作修改，请重新加载后再保存")

type Snapshot struct {
	Config          map[string]any `json:"config"`
	Version         string         `json:"version"`
	RequiresRestart bool           `json:"requires_restart"`
}
type ConfigStore struct {
	path string
	mu   sync.Mutex
}

func NewConfigStore(path string) *ConfigStore { return &ConfigStore{path: path} }
func version(data []byte) string              { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func secretKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "api_key")
}
func mask(obj map[string]any) {
	for key, value := range obj {
		if secretKey(key) {
			if text, ok := value.(string); ok && text != "" {
				obj[key] = Masked
			}
		}
		if child, ok := value.(map[string]any); ok {
			mask(child)
		}
	}
}
func restore(obj, previous map[string]any) error {
	for key, value := range obj {
		if secretKey(key) && value == Masked {
			old, ok := previous[key].(string)
			if !ok {
				return fmt.Errorf("%s: 新增配置需要填写真实值", key)
			}
			obj[key] = old
		} else if child, ok := value.(map[string]any); ok {
			old, _ := previous[key].(map[string]any)
			if err := restore(child, old); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *ConfigStore) Read() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return Snapshot{}, errors.New("无法读取配置文件")
	}
	var obj map[string]any
	if json.Unmarshal(data, &obj) != nil {
		return Snapshot{}, errors.New("配置文件不是有效 JSON")
	}
	mask(obj)
	return Snapshot{obj, version(data), true}, nil
}
func (s *ConfigStore) Save(expected string, obj map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, err := os.ReadFile(s.path)
	if err != nil {
		return errors.New("无法读取配置文件")
	}
	if expected == "" || expected != version(previous) {
		return ErrConflict
	}
	var old map[string]any
	if json.Unmarshal(previous, &old) != nil {
		return errors.New("现有配置无法解析")
	}
	if err = restore(obj, old); err != nil {
		return err
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return errors.New("配置不能序列化")
	}
	if err = appconfig.ValidateJSON(data); err != nil {
		return fmt.Errorf("配置校验失败：%w", err)
	}
	// Last check also detects external edits during validation. Operators should not
	// edit this file concurrently with UI saves; versioning protects stale clients.
	current, err := os.ReadFile(s.path)
	if err != nil || version(current) != expected {
		return ErrConflict
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".fastrag-config-*")
	if err != nil {
		return errors.New("配置目录不可写")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(append(data, '\n'))
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		return errors.New("保存配置失败，原文件保持不变")
	}
	if err = os.Rename(name, s.path); err != nil {
		return errors.New("替换配置文件失败")
	}
	return nil
}
