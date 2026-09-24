package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	Certificates map[string]CertificateState `json:"certificates"`
}

type CertificateState struct {
	LastRunAt   time.Time `json:"last_run_at"`
	LastResult  string    `json:"last_result"`
	LastError   string    `json:"last_error,omitempty"`
	NotAfter    time.Time `json:"not_after,omitempty"`
	LastVersion string    `json:"last_version,omitempty"`
}

func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{Certificates: map[string]CertificateState{}}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse state: %w", err)
	}
	if state.Certificates == nil {
		state.Certificates = map[string]CertificateState{}
	}
	return state, nil
}

func Save(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("activate state: %w", err)
	}
	return nil
}
