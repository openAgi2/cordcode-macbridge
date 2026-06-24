package gobridge

import (
	"github.com/openAgi2/cordcode-macbridge/core"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 数据目录布局：
// <dataDir>/identity.json — Bridge 身份（bridgeId, displayName, createdAt, runtimeVersion, protocol）
// <dataDir>/devices.json  — 受信设备（由 TrustedDeviceStore 管理，P0-04）
// <dataDir>/config.json   — 用户配置
// <dataDir>/pairing/      — 配对会话状态
// <dataDir>/logs/         — 运行时日志

// BridgeIdentity 存储 Bridge 的唯一身份信息。
type BridgeIdentity struct {
	BridgeID       string              `json:"bridgeId"`
	DisplayName    string              `json:"displayName"`
	CreatedAt      time.Time           `json:"createdAt"`
	RuntimeVersion string              `json:"runtimeVersion"`
	Protocol       BridgeIdentityProto `json:"protocol"`
}

// BridgeIdentityProto 描述 Bridge 使用的协议版本信息。
type BridgeIdentityProto struct {
	Name           string `json:"name"`
	Version        int    `json:"version"`
	SchemaRevision string `json:"schemaRevision"`
}

// ConfigData 保存任意用户配置，透传 JSON。
type ConfigData struct {
	Raw json.RawMessage `json:"-"`
}

// MarshalJSON 实现 json.Marshaler，将内部 RawMessage 直接输出。
func (c *ConfigData) MarshalJSON() ([]byte, error) {
	if c.Raw == nil {
		return []byte("{}"), nil
	}
	return c.Raw, nil
}

// UnmarshalJSON 实现 json.Unmarshaler，保留原始 JSON 字节。
func (c *ConfigData) UnmarshalJSON(data []byte) error {
	c.Raw = make(json.RawMessage, len(data))
	copy(c.Raw, data)
	return nil
}

// ConfigInvalidError 表示 config.json 内容格式不合法。
type ConfigInvalidError struct {
	Path    string
	Detail  string
	wrapped error
}

func (e *ConfigInvalidError) Error() string {
	return fmt.Sprintf("config_invalid: %s: %s", e.Path, e.Detail)
}

func (e *ConfigInvalidError) Unwrap() error {
	return e.wrapped
}

// DataDir 封装数据目录的路径，提供初始化、读写身份和配置的方法。
type DataDir struct {
	root string
}

// NewDataDir 创建一个 DataDir 实例。不执行 IO 操作。
func NewDataDir(path string) *DataDir {
	return &DataDir{root: path}
}

// Path 返回数据目录的根路径。
func (d *DataDir) Path() string {
	return d.root
}

// IdentityPath 返回 identity.json 的完整路径。
func (d *DataDir) IdentityPath() string {
	return filepath.Join(d.root, "identity.json")
}

// ConfigPath 返回 config.json 的完整路径。
func (d *DataDir) ConfigPath() string {
	return filepath.Join(d.root, "config.json")
}

// Initialize 创建所有子目录，生成 identity.json（如不存在），创建空 config.json（如不存在）。
// 该方法是幂等的：重复调用不会覆盖已有的 identity.json 或 config.json。
func (d *DataDir) Initialize() error {
	// 创建根目录和子目录
	if err := d.EnsureSubdirs(); err != nil {
		return fmt.Errorf("初始化数据目录失败: %w", err)
	}

	// 生成 identity.json（仅在文件不存在时）
	if _, err := os.Stat(d.IdentityPath()); os.IsNotExist(err) {
		id := &BridgeIdentity{
			BridgeID:       GenerateBridgeID(),
			DisplayName:    "CordCode Link",
			CreatedAt:      time.Now().UTC(),
			RuntimeVersion: runtimeVersion,
			Protocol: BridgeIdentityProto{
				Name:           BridgeProtocolName,
				Version:        BridgeProtocolVersion,
				SchemaRevision: BridgeProtocolSchemaRevision,
			},
		}
		if err := d.WriteIdentity(id); err != nil {
			return fmt.Errorf("写入 identity.json 失败: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("检查 identity.json 失败: %w", err)
	}

	// 创建空 config.json（仅在文件不存在时）
	if _, err := os.Stat(d.ConfigPath()); os.IsNotExist(err) {
		empty := &ConfigData{Raw: json.RawMessage(`{}`)}
		if err := d.WriteConfig(empty); err != nil {
			return fmt.Errorf("写入 config.json 失败: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("检查 config.json 失败: %w", err)
	}

	return nil
}

// ReadIdentity 读取并解析 identity.json。
func (d *DataDir) ReadIdentity() (*BridgeIdentity, error) {
	data, err := os.ReadFile(d.IdentityPath())
	if err != nil {
		return nil, fmt.Errorf("读取 identity.json 失败: %w", err)
	}
	var id BridgeIdentity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, fmt.Errorf("解析 identity.json 失败: %w", err)
	}
	return &id, nil
}

// WriteIdentity 将 BridgeIdentity 写入 identity.json。
func (d *DataDir) WriteIdentity(id *BridgeIdentity) error {
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 identity 失败: %w", err)
	}
	data = append(data, '\n')
	// 原子写 + 0600（P2-5）。
	return core.AtomicWriteFile(d.IdentityPath(), data, 0o600)
}

// ReadConfig 读取 config.json，内容不合法时返回 ConfigInvalidError。
func (d *DataDir) ReadConfig() (*ConfigData, error) {
	data, err := os.ReadFile(d.ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("读取 config.json 失败: %w", err)
	}
	if !json.Valid(data) {
		return nil, &ConfigInvalidError{
			Path:   d.ConfigPath(),
			Detail: "invalid JSON",
		}
	}
	var c ConfigData
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, &ConfigInvalidError{
			Path:    d.ConfigPath(),
			Detail:  err.Error(),
			wrapped: err,
		}
	}
	return &c, nil
}

// WriteConfig 将 ConfigData 写入 config.json。
func (d *DataDir) WriteConfig(c *ConfigData) error {
	var data []byte
	var err error
	if c == nil || c.Raw == nil {
		data = []byte("{}\n")
	} else {
		data, err = json.MarshalIndent(c, "", "  ")
		if err != nil {
			return fmt.Errorf("序列化 config 失败: %w", err)
		}
		data = append(data, '\n')
	}
	// 原子写 + 0600（P2-5）。
	return core.AtomicWriteFile(d.ConfigPath(), data, 0o600)
}

// EnsureSubdirs 创建 pairing/ 和 logs/ 子目录（如不存在）。
func (d *DataDir) EnsureSubdirs() error {
	subdirs := []string{
		filepath.Join(d.root, "pairing"),
		filepath.Join(d.root, "logs"),
	}
	for _, dir := range subdirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建子目录 %s 失败: %w", dir, err)
		}
	}
	return nil
}

// GenerateBridgeID 生成 brg_ 前缀的唯一 ID，使用 crypto/rand + base64url（16 字节）。
func GenerateBridgeID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read 在正确使用的系统上不会返回错误，
		// 如果真的失败了说明系统熵源有严重问题，直接 panic。
		panic("crypto/rand.Read 失败: " + err.Error())
	}
	return "brg_" + base64.RawURLEncoding.EncodeToString(b)
}
