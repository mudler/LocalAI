package studio

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrMemorySettings means the memory settings cannot be stored.
var ErrMemorySettings = errors.New("studio: the memory settings cannot be stored on this server")

// MemorySettings is the person's own memory choices, as the Memory tab shows
// them. The server keeps them with the person's other settings and every
// frontend reads the same answer.
type MemorySettings struct {
	// Enabled is the switch in force: memory On or Off for every chat of the
	// person. A chat, a messaging connection or an agent can only narrow it.
	Enabled bool `json:"enabled"`
	// EnabledSet is true once the person chose. Before that the switch is on,
	// unless they had disabled the Memory add-on to stop memory.
	EnabledSet bool `json:"enabled_set"`
	// SaveMode is how a note is saved: ask, auto, always or never.
	SaveMode string `json:"save_mode"`
	// MessagingSave is how a note proposed in a messaging chat is saved: ask,
	// paired_private or never. It never loosens always or never in SaveMode.
	MessagingSave string `json:"messaging_save"`
	// SaveModeSet is true once the person chose in this setting. Before that
	// the value they gave on the page of the Memory add-on, if any, applies.
	SaveModeSet bool `json:"save_mode_set"`
	// Provider says what serves memory: "builtin", the ID of an add-on, or
	// "off" (see GET /api/chats/providers for the list to pick from).
	Provider string `json:"provider"`
	// Available is false when the server has no files: notes are files.
	Available bool `json:"available"`
}

// MemoryUpdate is a change to the settings. A nil field is left alone.
type MemoryUpdate struct {
	Enabled  *bool
	SaveMode *string
	// MessagingSave is the messaging row: ask, paired_private or never.
	MessagingSave *string
}

// MemorySettings reads the memory settings of a person.
func (s *Service) MemorySettings(ctx context.Context, user string) (MemorySettings, error) {
	legacy := legacyMemoryOf(s.addonContextSets(ctx, user))
	res := s.memoryResolution(ctx, user, legacy)
	out := MemorySettings{Provider: res.Active, Available: s.d.Files.Available(), SaveMode: SaveAsk, MessagingSave: MessagingAsk}
	out.Enabled = !res.Off
	out.EnabledSet = res.Stored
	if s.d.UserSetting != nil {
		if v, ok, err := s.d.UserSetting(ctx, principal(user), HostSettingsID, MemoryEnabledKey); err == nil && ok && (v == MemoryOn || v == MemoryOff) {
			out.EnabledSet = true
		}
		if v, ok, err := s.d.UserSetting(ctx, principal(user), HostSettingsID, MemorySaveKey); err == nil && ok && ValidSaveMode(v) {
			out.SaveModeSet = true
		}
	}
	out.SaveMode = s.changes.storedSaveMode(ctx, user)
	out.MessagingSave = s.changes.storedMessagingSave(ctx, user)
	return out, nil
}

// SetMemory stores the choices of a person. A value that is not one of the
// allowed ones is ErrInvalidSettings.
func (s *Service) SetMemory(ctx context.Context, user string, in MemoryUpdate) (MemorySettings, error) {
	if in.SaveMode != nil && !ValidSaveMode(*in.SaveMode) {
		return MemorySettings{}, fmt.Errorf("%w: save_mode must be ask, auto, always or never", ErrInvalidSettings)
	}
	if in.MessagingSave != nil && !ValidMessagingSave(*in.MessagingSave) {
		return MemorySettings{}, fmt.Errorf("%w: messaging_save must be ask, paired_private or never", ErrInvalidSettings)
	}
	if (in.Enabled != nil || in.SaveMode != nil || in.MessagingSave != nil) && s.d.SetUserSetting == nil {
		return MemorySettings{}, ErrMemorySettings
	}
	if in.Enabled != nil {
		v := MemoryOff
		if *in.Enabled {
			v = MemoryOn
		}
		if err := s.d.SetUserSetting(ctx, principal(user), HostSettingsID, MemoryEnabledKey, v); err != nil {
			return MemorySettings{}, fmt.Errorf("%w: %v", ErrMemorySettings, err)
		}
		// The switch and the provider choice are one answer. Off picks Off. On
		// takes Off back to the built-in provider and keeps an add-on that was
		// picked.
		choice, chosen, err := s.storedProvider(ctx, user, MemorySlot)
		if err != nil {
			return MemorySettings{}, fmt.Errorf("%w: %v", ErrMemorySettings, err)
		}
		next := ""
		switch {
		case !*in.Enabled:
			next = contract.ProviderOff
		case chosen && choice == contract.ProviderOff:
			next = contract.ProviderBuiltin
		}
		if next != "" {
			if err := s.d.SetUserSetting(ctx, principal(user), HostSettingsID, providerKey(MemorySlot), next); err != nil {
				return MemorySettings{}, fmt.Errorf("%w: %v", ErrMemorySettings, err)
			}
		}
	}
	if in.SaveMode != nil {
		if err := s.d.SetUserSetting(ctx, principal(user), HostSettingsID, MemorySaveKey, *in.SaveMode); err != nil {
			return MemorySettings{}, fmt.Errorf("%w: %v", ErrMemorySettings, err)
		}
	}
	if in.MessagingSave != nil {
		if err := s.d.SetUserSetting(ctx, principal(user), HostSettingsID, MemorySaveMessagingKey, *in.MessagingSave); err != nil {
			return MemorySettings{}, fmt.Errorf("%w: %v", ErrMemorySettings, err)
		}
	}
	return s.MemorySettings(ctx, user)
}

// ErrNoNotes means a scope has no notes to export.
var ErrNoNotes = errors.New("studio: there are no notes to export")

// ExportMemory writes the notes of a person as a zip file: the Markdown files
// of the memory folders, with their paths in the home. scope and key narrow it
// to one group, as the context files list names them (user; agent and the
// agent ID; channel and the folder name); empty exports all. It works while
// memory is off: the notes belong to the person.
func (s *Service) ExportMemory(ctx context.Context, user string, w io.Writer, scope, key string) (int, error) {
	// A provider that serves the notes keeps them: its export is the export.
	if res := s.memoryResolution(ctx, user, legacyMemoryOf(s.addonContextSets(ctx, user))); res.Addon() != "" {
		return s.exportFromProvider(ctx, user, res, w)
	}
	list, err := s.ListContextFiles(ctx, user)
	if err != nil {
		return 0, err
	}
	var paths []string
	for _, g := range list.Groups {
		if g.Source != MemoryAddonID {
			continue
		}
		if scope != "" && (string(g.Scope) != scope || g.Key != key) {
			continue
		}
		for _, f := range g.Files {
			paths = append(paths, f.Path)
		}
	}
	if len(paths) == 0 {
		return 0, ErrNoNotes
	}
	zw := zip.NewWriter(w)
	rd := homeReader{f: s.d.Files}
	n := 0
	for _, p := range paths {
		data, err := rd.ReadFile(ctx, principal(user), p, 1<<20)
		if err != nil {
			if errors.Is(err, contextfiles.ErrNotExist) {
				continue
			}
			return n, err
		}
		fw, err := zw.Create(strings.TrimPrefix(path.Clean(p), "/"))
		if err != nil {
			return n, err
		}
		if _, err := fw.Write(data); err != nil {
			return n, err
		}
		n++
	}
	return n, zw.Close()
}
