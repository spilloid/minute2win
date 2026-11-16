package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type mcpSettings struct {
	ImageBase     string `json:"image_base"`
	LinuxAssets   string `json:"linux_assets"`
	ArmTTLSeconds int64  `json:"arm_ttl_seconds"`
}

func readMCPSettings(path string) (*mcpSettings, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c mcpSettings
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.ImageBase != "" {
		if err := checkImageBase(c.ImageBase); err != nil {
			return nil, err
		}
	}
	if c.ArmTTLSeconds < 60 || c.ArmTTLSeconds > 86400 {
		return nil, fmt.Errorf("saved MCP arm TTL must be 60 to 86400 seconds")
	}
	return &c, nil
}
func writeMCPSettings(path string, c mcpSettings) error {
	if path == "" {
		return nil
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return atomicRootWrite(root, filepath.Base(path), b)
}
