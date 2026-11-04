// SPDX-License-Identifier: MIT

// Package settings keeps the per-device switches of the PiKVM add-on: whether
// a model may ask for a hard power action, whether it may type, and how long a
// text may be. All of them are off or small by default, and an administrator
// changes them on the add-on page. The file is keyed by the address of the
// device, the same address the connection lists as its resource.
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
)

// Limits of the text a model may type.
const (
	DefaultTypeChars = 200
	MaxTypeChars     = 1000
	// MaxDevices bounds the file.
	MaxDevices = 50
)

// Device is what one device allows.
type Device struct {
	AllowHardPower bool `json:"allowHardPower"`
	AllowTyping    bool `json:"allowTyping"`
	MaxTypeChars   int  `json:"maxTypeChars"`
}

// Defaults is what a device allows until an administrator says otherwise.
func Defaults() Device { return Device{MaxTypeChars: DefaultTypeChars} }

// Normalize fixes the length limit into its range.
func (d Device) Normalize() Device {
	if d.MaxTypeChars <= 0 {
		d.MaxTypeChars = DefaultTypeChars
	}
	d.MaxTypeChars = min(d.MaxTypeChars, MaxTypeChars)
	return d
}

type file struct {
	Devices map[string]Device `json:"devices"`
}

// Store keeps the settings in settings.json in dir, mode 0600, written
// atomically. The file is read on each call.
type Store struct {
	mu   sync.Mutex
	path string
	mem  file
}

// Open returns a store in dir. An empty dir keeps the settings in memory.
func Open(dir string) *Store {
	s := &Store{mem: file{Devices: map[string]Device{}}}
	if dir != "" {
		s.path = filepath.Join(dir, "settings.json")
	}
	return s
}

func (s *Store) read() (file, error) {
	if s.path == "" {
		return s.mem, nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return file{Devices: map[string]Device{}}, nil
	}
	if err != nil {
		return file{}, errors.New("the settings file cannot be read")
	}
	var f file
	if json.Unmarshal(b, &f) != nil {
		return file{}, errors.New("the settings file is damaged")
	}
	if f.Devices == nil {
		f.Devices = map[string]Device{}
	}
	return f, nil
}

// For returns the settings of one device. A file that cannot be read gives the
// defaults with an error: the caller treats everything as off.
func (s *Store) For(key string) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return Defaults(), err
	}
	d, ok := f.Devices[key]
	if !ok {
		return Defaults(), nil
	}
	return d.Normalize(), nil
}

// All returns the settings of every device that has some.
func (s *Store) All() (map[string]Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return map[string]Device{}, err
	}
	out := make(map[string]Device, len(f.Devices))
	for k, d := range f.Devices {
		out[k] = d.Normalize()
	}
	return out, nil
}

// Set stores the settings of one device. A damaged file is replaced.
func (s *Store) Set(key string, d Device) (Device, error) {
	d = d.Normalize()
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		f = file{Devices: map[string]Device{}}
	}
	if _, known := f.Devices[key]; !known && len(f.Devices) >= MaxDevices {
		return Device{}, errors.New("too many devices have settings")
	}
	f.Devices[key] = d
	if s.path == "" {
		s.mem = f
		return d, nil
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return Device{}, err
	}
	if err := atomicfile.Write(s.path, b, 0o600); err != nil {
		return Device{}, errors.New("the settings could not be saved")
	}
	return d, nil
}
