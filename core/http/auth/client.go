// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rand"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KeyKindClient marks the API key of a paired client. A client key is a
// row of the API key table, but it is not a user key: it authenticates only
// the routes that name one of its features, and it never gets the role of the
// owner who paired it.
const KeyKindClient = "client"

// Features of a client key. They are not in AllFeatures, so no user can be
// granted them and no user-facing permission screen lists them. A client key
// holds exactly the features of its allow-list and nothing else: a route that
// is not in RouteFeatureRegistry with one of these features is refused.
const (
	// FeatureClientSelf lets a client read its own identity and state.
	FeatureClientSelf = "client_self"
	// FeatureClientAddons lets a client call the routes that an installed
	// add-on declares for clients (/api/client/addons/:addon/...).
	FeatureClientAddons = "client_addons"
	// FeatureClientInference lets a client use the standard inference API with
	// its own key. The routes it reaches are the rows of RouteFeatureRegistry
	// that name it, and no other: that list is frozen by a test, so a route
	// added later does not widen every key that holds the feature.
	FeatureClientInference = "client_inference"
	// FeatureClientFolders switches on the folder grant of a key: the folders of
	// the owner's workspace that the key may read and the one it may write,
	// reached by an add-on through the client.workspace.* host operations. It
	// opens no route by itself; it only says that the grant of the key counts.
	FeatureClientFolders = "client_folders"
)

// ClientFeatures lists every feature that a client key can hold.
var ClientFeatures = []string{FeatureClientSelf, FeatureClientAddons, FeatureClientInference, FeatureClientFolders}

// sharedClientFeatures are the client features whose routes are also routes of
// users. A row with one of them only adds a way in for a client key; it does
// not gate the route for a user, who keeps the user feature of the route (or
// none). The other client features name routes that only a client key uses.
var sharedClientFeatures = []string{FeatureClientInference}

// IsClientFeature reports whether the feature belongs to client keys.
func IsClientFeature(feature string) bool { return slices.Contains(ClientFeatures, feature) }

const (
	// pairingCodeAlphabet has no 0, O, 1, I or L, so a code read aloud or from
	// a small screen is hard to get wrong. 31 symbols over 10 places is about
	// 49 bits.
	pairingCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	pairingCodeLen      = 10

	// DefaultPairingCodeTTL and MaxPairingCodeTTL bound the life of a code.
	DefaultPairingCodeTTL = 10 * time.Minute
	MaxPairingCodeTTL     = time.Hour
	// MaxPendingPairingCodes is the number of unused, unexpired codes one
	// owner may hold. It bounds the guessing surface.
	MaxPendingPairingCodes = 5
	// MaxClientKeysPerOwner bounds the paired clients of one owner.
	MaxClientKeysPerOwner = 32

	pairingHashDomain = "client-pairing-code:"
)

// ErrPairingInvalid is the one error of a failed redemption. It does not say
// whether the code was unknown, expired or used.
var ErrPairingInvalid = errors.New("invalid or expired pairing code")

// ErrTooManyPairingCodes and ErrTooManyClients report the per-owner limits.
var (
	ErrTooManyPairingCodes = errors.New("too many pending pairing codes")
	ErrTooManyClients      = errors.New("too many paired clients")
)

// ClientPairingCode is a one-time code an owner creates so that one client can
// obtain its credential. Only the hash of the code is stored.
type ClientPairingCode struct {
	ID       string        `gorm:"primaryKey;size:36"`
	CodeHash string        `gorm:"size:64;uniqueIndex;not null"`
	OwnerID  string        `gorm:"size:36;index;not null"`
	Name     string        `gorm:"size:255"` // label of the client the code is for
	Features PermissionMap `gorm:"type:text"`
	Addons   AddonList     `gorm:"type:text"` // add-ons the issued key may reach
	Models   ModelList     `gorm:"type:text"` // models the issued key may use (client_inference)
	// ReadFolders and WriteFolder are the folder grant of the issued key
	// (client_folders), relative to the workspace of the owner.
	ReadFolders FolderList `gorm:"type:text"`
	WriteFolder string     `gorm:"size:255"`
	KeyTTL      int64      // life of the issued key in seconds; 0 means no expiry
	ExpiresAt   time.Time  `gorm:"index;not null"`
	UsedAt      *time.Time
	CreatedAt   time.Time
}

// ErrInvalidClientInput matches every refusal of the input of a pairing code or
// of a client key: an unknown feature, a bad add-on id, too many add-ons, or a
// list that does not fit the features. A handler answers 400 for it.
var ErrInvalidClientInput = errors.New("invalid client input")

// clientInputError is an input refusal with its own words that still matches
// ErrInvalidClientInput.
type clientInputError struct{ msg string }

func (e *clientInputError) Error() string        { return e.msg }
func (e *clientInputError) Is(target error) bool { return target == ErrInvalidClientInput }

func invalidClientInput(format string, args ...any) error {
	return &clientInputError{msg: fmt.Sprintf(format, args...)}
}

// NormalizeClientFeatures validates a feature list and returns it as a map.
// An empty list gets the minimal default, FeatureClientSelf. A name that is not
// a client feature is an error, so a code can never carry a user feature.
func NormalizeClientFeatures(features []string) (PermissionMap, error) {
	m := PermissionMap{}
	for _, f := range features {
		if !IsClientFeature(f) {
			return nil, invalidClientInput("unknown client feature %q", f)
		}
		m[f] = true
	}
	if len(m) == 0 {
		m[FeatureClientSelf] = true
	}
	return m, nil
}

// AddonList is a list of add-on ids stored as JSON text.
type AddonList []string

// Value implements driver.Valuer.
func (l AddonList) Value() (driver.Value, error) { return listValue(l) }

// Scan implements sql.Scanner.
func (l *AddonList) Scan(value any) error { return listScan((*[]string)(l), value) }

// ModelList is a list of model names stored as JSON text.
type ModelList []string

// Value implements driver.Valuer.
func (l ModelList) Value() (driver.Value, error) { return listValue(l) }

// Scan implements sql.Scanner.
func (l *ModelList) Scan(value any) error { return listScan((*[]string)(l), value) }

func listValue(l []string) (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	b, err := json.Marshal(l)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func listScan(dst *[]string, value any) error {
	var b []byte
	switch v := value.(type) {
	case nil:
		*dst = nil
		return nil
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("cannot scan %T into a list", value)
	}
	if len(b) == 0 {
		*dst = nil
		return nil
	}
	return json.Unmarshal(b, dst)
}

// MaxClientAddons bounds the add-ons of one key.
const MaxClientAddons = 16

// NormalizeClientAddons validates the add-on ids of a code or a key: each must
// be a well-formed add-on id that no host route uses. It removes duplicates and
// sorts the list. Whether an add-on is installed is not checked: an add-on can
// be installed after the pairing, and the list only ever narrows what a key
// reaches.
func NormalizeClientAddons(addons []string) (AddonList, error) {
	seen := map[string]bool{}
	out := AddonList{}
	for _, a := range addons {
		if !contract.ValidAddonID(a) {
			return nil, invalidClientInput("invalid add-on id %q", a)
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	if len(out) > MaxClientAddons {
		return nil, invalidClientInput("at most %d add-ons", MaxClientAddons)
	}
	slices.Sort(out)
	return out, nil
}

// MaxClientModels bounds the models of one key, and MaxClientModelName bounds
// the length of one name.
const (
	MaxClientModels    = 32
	MaxClientModelName = 255
)

// NormalizeClientModels validates the model names of a code or a key: each must
// be non-empty, at most MaxClientModelName bytes, with no control character and
// no space at either end. It removes duplicates and sorts the list. Whether a
// model exists is not checked here (see ClientModelCheck).
func NormalizeClientModels(models []string) (ModelList, error) {
	seen := map[string]bool{}
	out := ModelList{}
	for _, m := range models {
		if m == "" || len(m) > MaxClientModelName || strings.TrimSpace(m) != m || strings.ContainsFunc(m, unicode.IsControl) {
			return nil, invalidClientInput("invalid model name %q", utils.TruncateUTF8(m, 64))
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(out) > MaxClientModels {
		return nil, invalidClientInput("at most %d models", MaxClientModels)
	}
	slices.Sort(out)
	return out, nil
}

// ClientModelCheck says whether a model may be on the list of a client key. It
// returns nil when it may, and otherwise the reason. LocalAI builds one from
// the model configurations: the model must exist and have no tools attached.
type ClientModelCheck func(model string) error

func checkClientModels(models ModelList, check ClientModelCheck) error {
	if check == nil {
		return nil
	}
	for _, m := range models {
		if err := check(m); err != nil {
			return invalidClientInput("model %q: %v", m, err)
		}
	}
	return nil
}

// ErrClientModels is the refusal of a model list that does not fit the
// features: client_inference needs at least one model, and a list without that
// feature has no use.
var ErrClientModels error = &clientInputError{msg: "client_inference needs a list of models, and only client_inference takes one"}

// PairingOption sets an optional field of a pairing code.
type PairingOption func(*pairingOptions)

type pairingOptions struct {
	models      []string
	modelCheck  ClientModelCheck
	readFolders []string
	writeFolder string
}

// WithFolders sets the folder grant of the issued key, for a code that carries
// client_folders.
func WithFolders(read []string, write string) PairingOption {
	return func(o *pairingOptions) { o.readFolders, o.writeFolder = read, write }
}

// WithModels sets the models the issued key may use, for a code that carries
// client_inference.
func WithModels(models []string) PairingOption {
	return func(o *pairingOptions) { o.models = models }
}

// WithModelCheck makes CreatePairingCode check each listed model.
func WithModelCheck(check ClientModelCheck) PairingOption {
	return func(o *pairingOptions) { o.modelCheck = check }
}

// ErrClientAddons is the refusal of an add-on list that does not fit the
// features: client_addons needs at least one add-on, and a list without that
// feature has no use.
var ErrClientAddons error = &clientInputError{msg: "client_addons needs a list of add-ons, and only client_addons takes one"}

func newPairingCode() (string, error) {
	b := make([]byte, pairingCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}
	out := make([]byte, 0, pairingCodeLen+1)
	for i, v := range b {
		// 248 = 8*31 is the largest multiple of 31 under 256, so the modulo
		// below does not favour any symbol; resample the rest.
		for v >= 248 {
			var r [1]byte
			if _, err := rand.Read(r[:]); err != nil {
				return "", fmt.Errorf("generate pairing code: %w", err)
			}
			v = r[0]
		}
		if i == pairingCodeLen/2 {
			out = append(out, '-')
		}
		out = append(out, pairingCodeAlphabet[int(v)%len(pairingCodeAlphabet)])
	}
	return string(out), nil
}

// PairingCodeWellFormed reports whether the text has the shape of a code: the
// right number of symbols from the alphabet, with or without the dash.
func PairingCodeWellFormed(code string) bool {
	n := NormalizePairingCode(code)
	if len(n) != pairingCodeLen {
		return false
	}
	for i := 0; i < len(n); i++ {
		if !strings.ContainsRune(pairingCodeAlphabet, rune(n[i])) {
			return false
		}
	}
	return true
}

// NormalizePairingCode upper-cases a code and drops separators and spaces.
func NormalizePairingCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hashPairingCode(code, hmacSecret string) string {
	return HashAPIKey(pairingHashDomain+NormalizePairingCode(code), hmacSecret)
}

// CreatePairingCode stores a new code for the owner and returns the plaintext,
// which is shown once. ttl is clamped to MaxPairingCodeTTL; keyTTL of zero
// makes a key that does not expire (it can still be revoked).
func CreatePairingCode(db *gorm.DB, ownerID, name string, features, addons []string, ttl, keyTTL time.Duration, hmacSecret string, opts ...PairingOption) (string, *ClientPairingCode, error) {
	var po pairingOptions
	for _, o := range opts {
		o(&po)
	}
	perms, err := NormalizeClientFeatures(features)
	if err != nil {
		return "", nil, err
	}
	list, err := NormalizeClientAddons(addons)
	if err != nil {
		return "", nil, err
	}
	// The list and the feature go together: a code that reaches add-ons names
	// them, and a code that names add-ons reaches them.
	if perms[FeatureClientAddons] != (len(list) > 0) {
		return "", nil, ErrClientAddons
	}
	models, err := NormalizeClientModels(po.models)
	if err != nil {
		return "", nil, err
	}
	// The same rule for models, and no model can be listed until it passes the
	// check of LocalAI: it exists and has no tools attached.
	if perms[FeatureClientInference] != (len(models) > 0) {
		return "", nil, ErrClientModels
	}
	if err := checkClientModels(models, po.modelCheck); err != nil {
		return "", nil, err
	}
	// The same rule for folders: the feature and the grant go together.
	readFolders, writeFolder, err := NormalizeClientFolders(po.readFolders, po.writeFolder)
	if err != nil {
		return "", nil, err
	}
	if perms[FeatureClientFolders] != (len(readFolders) > 0 || writeFolder != "") {
		return "", nil, ErrClientFolders
	}
	if ttl <= 0 {
		ttl = DefaultPairingCodeTTL
	}
	ttl = min(ttl, MaxPairingCodeTTL)
	if keyTTL < 0 {
		keyTTL = 0
	}
	now := time.Now()
	// Expired codes are dead weight; drop them as the owner creates a new one.
	if err := db.Where("expires_at < ?", now).Delete(&ClientPairingCode{}).Error; err != nil {
		return "", nil, err
	}
	var pending int64
	if err := db.Model(&ClientPairingCode{}).
		Where("owner_id = ? AND used_at IS NULL AND expires_at > ?", ownerID, now).
		Count(&pending).Error; err != nil {
		return "", nil, err
	}
	if pending >= MaxPendingPairingCodes {
		return "", nil, ErrTooManyPairingCodes
	}
	code, err := newPairingCode()
	if err != nil {
		return "", nil, err
	}
	rec := &ClientPairingCode{
		ID:          uuid.New().String(),
		CodeHash:    hashPairingCode(code, hmacSecret),
		OwnerID:     ownerID,
		Name:        utils.TruncateUTF8(name, 255),
		Features:    perms,
		Addons:      list,
		Models:      models,
		ReadFolders: readFolders,
		WriteFolder: writeFolder,
		KeyTTL:      int64(keyTTL / time.Second),
		ExpiresAt:   now.Add(ttl),
	}
	if err := db.Create(rec).Error; err != nil {
		return "", nil, fmt.Errorf("store pairing code: %w", err)
	}
	return code, rec, nil
}

// ListPairingCodes returns the unused, unexpired codes of an owner.
func ListPairingCodes(db *gorm.DB, ownerID string) ([]ClientPairingCode, error) {
	var out []ClientPairingCode
	err := db.Where("owner_id = ? AND used_at IS NULL AND expires_at > ?", ownerID, time.Now()).
		Order("created_at DESC").Find(&out).Error
	return out, err
}

// DeletePairingCode cancels a code of the owner.
func DeletePairingCode(db *gorm.DB, id, ownerID string) error {
	res := db.Where("id = ? AND owner_id = ?", id, ownerID).Delete(&ClientPairingCode{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// RedeemPairingCode turns a valid code into a client key. The claim is one
// conditional UPDATE, so two clients that post the same code at once cannot
// both win. Every failure returns ErrPairingInvalid.
func RedeemPairingCode(db *gorm.DB, code, clientName, hmacSecret string) (string, *UserAPIKey, error) {
	if len(NormalizePairingCode(code)) != pairingCodeLen {
		return "", nil, ErrPairingInvalid
	}
	hash := hashPairingCode(code, hmacSecret)
	var plaintext string
	var key *UserAPIKey
	err := db.Transaction(func(tx *gorm.DB) error {
		var rec ClientPairingCode
		if err := tx.Where("code_hash = ?", hash).First(&rec).Error; err != nil {
			return ErrPairingInvalid
		}
		now := time.Now()
		res := tx.Model(&ClientPairingCode{}).
			Where("id = ? AND used_at IS NULL AND expires_at > ?", rec.ID, now).
			Update("used_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrPairingInvalid
		}
		var owner User
		// The row of the owner is locked until the end of the transaction (a
		// no-op on SQLite, which has one writer), so two codes of one owner
		// redeemed at once count the clients one after the other.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", rec.OwnerID, StatusActive).First(&owner).Error; err != nil {
			return ErrPairingInvalid
		}
		// An expired key is dead weight: remove it so it does not use a place.
		if err := tx.Where("user_id = ? AND kind = ? AND expires_at IS NOT NULL AND expires_at < ?", owner.ID, KeyKindClient, now).Delete(&UserAPIKey{}).Error; err != nil {
			return err
		}
		var clients int64
		if err := tx.Model(&UserAPIKey{}).Where("user_id = ? AND kind = ?", owner.ID, KeyKindClient).Count(&clients).Error; err != nil {
			return err
		}
		if clients >= MaxClientKeysPerOwner {
			return ErrTooManyClients
		}
		name := strings.TrimSpace(clientName)
		if name == "" {
			name = rec.Name
		}
		if name == "" {
			name = "client"
		}
		name = utils.TruncateUTF8(name, 255)
		var expires *time.Time
		if rec.KeyTTL > 0 {
			t := now.Add(time.Duration(rec.KeyTTL) * time.Second)
			expires = &t
		}
		p, h, prefix, err := GenerateAPIKey(hmacSecret)
		if err != nil {
			return err
		}
		k := &UserAPIKey{
			ID:        uuid.New().String(),
			UserID:    owner.ID,
			Name:      name,
			KeyHash:   h,
			KeyPrefix: prefix,
			// Never the role of the owner: the key is not a stand-in for the user.
			Role:        RoleUser,
			Kind:        KeyKindClient,
			Features:    rec.Features,
			Addons:      rec.Addons,
			Models:      rec.Models,
			ReadFolders: rec.ReadFolders,
			WriteFolder: rec.WriteFolder,
			ExpiresAt:   expires,
		}
		if err := tx.Create(k).Error; err != nil {
			return fmt.Errorf("store client key: %w", err)
		}
		plaintext, key = p, k
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return plaintext, key, nil
}

// ListClientKeys returns the client keys of an owner, without secrets.
func ListClientKeys(db *gorm.DB, ownerID string) ([]UserAPIKey, error) {
	var keys []UserAPIKey
	err := db.Where("user_id = ? AND kind = ?", ownerID, KeyKindClient).Order("created_at DESC").Find(&keys).Error
	return keys, err
}

// ClientDisplayName is the label of a client as an add-on sees it. A client
// chooses its own name when it pairs, so the label keeps the printable
// characters of any script, drops control characters and invalid bytes, and is
// cut at 64 bytes on a character boundary.
func ClientDisplayName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r != utf8.RuneError && unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return utils.TruncateUTF8(b.String(), 64)
}

// ActiveClientKeysForAddon returns the client keys of the owner that stand now
// and may reach the add-on: not expired, not paused, with an owner that is an
// active administrator, holding client_addons with the add-on in the list.
// Nothing here is a credential: callers copy the id and the name only.
func ActiveClientKeysForAddon(db *gorm.DB, ownerID, addonID string, now time.Time) ([]UserAPIKey, error) {
	if db == nil || ownerID == "" || addonID == "" {
		return nil, nil
	}
	var keys []UserAPIKey
	err := db.Preload("User").Where("user_id = ? AND kind = ?", ownerID, KeyKindClient).Order("created_at DESC").Find(&keys).Error
	if err != nil {
		return nil, err
	}
	out := keys[:0]
	for _, k := range keys {
		if k.CheckUsable(now) == nil && k.AllowsAddon(addonID) {
			out = append(out, k)
		}
	}
	return out, nil
}

// RevokeClientKey deletes a client key of the owner. The key stops working on
// its next request: every request loads the key from the store.
func RevokeClientKey(db *gorm.DB, id, ownerID string) error {
	res := db.Where("id = ? AND user_id = ? AND kind = ?", id, ownerID, KeyKindClient).Delete(&UserAPIKey{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// IsClientKey reports whether the key is a client key.
func (k *UserAPIKey) IsClientKey() bool { return k != nil && k.Kind == KeyKindClient }

// ClientAllows reports whether a client key holds the feature. A key that is
// not a client key, a feature that is not a client feature and an absent entry
// are all refusals: the allow-list is the only source.
func (k *UserAPIKey) ClientAllows(feature string) bool {
	return k.IsClientKey() && IsClientFeature(feature) && k.Features[feature]
}

// ClientKey returns the client key that authenticated the request, or nil.
func ClientKey(c echo.Context) *UserAPIKey {
	if k := GetAPIKey(c); k.IsClientKey() {
		return k
	}
	return nil
}

// ClientKeyActive reports whether the client key still stands for a paired
// client of the owner: it exists, is not expired and is not paused, and the
// owner is an active account. A host
// operation that acts for a client asks this each time, so a revoked client
// loses the jobs it could start at once, even inside a stream that is open. A
// failed query counts as not active; ClientKeyStatus tells the two apart.
func ClientKeyActive(db *gorm.DB, keyID, ownerID string) bool {
	active, err := ClientKeyStatus(db, keyID, ownerID)
	return err == nil && active
}

// ClientKeyStatus is ClientKeyActive with the error of the store kept apart: a
// key that is gone, expired or paused, or an owner that is not active, answers
// (false, nil); a query that failed answers (false, err), and the key may well
// still be good. A caller that holds a long connection open uses it so that a
// blip of the store does not cut the connection.
func ClientKeyStatus(db *gorm.DB, keyID, ownerID string) (bool, error) {
	return ClientKeyStatusFor(context.Background(), db, keyID, ownerID, "", nil)
}

// ClientKeyStatusFor is ClientKeyStatus for a long connection that was opened
// for one model. The query carries ctx, so a store that hangs ends with the
// context and not never. When model is not empty the key must still list it
// and the check (which may be nil) must still pass, so a list that changes
// during the session ends it.
func ClientKeyStatusFor(ctx context.Context, db *gorm.DB, keyID, ownerID, model string, check ClientModelCheck) (bool, error) {
	if db == nil || keyID == "" || ownerID == "" {
		return false, nil
	}
	// One query loads the key with its owner: a disabled owner stops the
	// streams and the host operations of every client it paired.
	var k UserAPIKey
	if err := db.WithContext(ctx).Preload("User").Where("id = ? AND user_id = ? AND kind = ?", keyID, ownerID, KeyKindClient).First(&k).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if k.CheckUsable(time.Now()) != nil {
		return false, nil
	}
	if model != "" {
		if !k.AllowsModel(model) {
			return false, nil
		}
		if check != nil && check(model) != nil {
			return false, nil
		}
	}
	return true, nil
}

// AllowsAddon reports whether a client key may reach the add-on. It needs the
// client_addons feature and the id in the list of the key. An empty list
// reaches nothing.
func (k *UserAPIKey) AllowsAddon(addon string) bool {
	return k.ClientAllows(FeatureClientAddons) && slices.Contains(k.Addons, addon)
}

// SetClientKeyAddons replaces the add-on list of a client key of the owner. An
// empty list is allowed and cuts the key off from every add-on.
func SetClientKeyAddons(db *gorm.DB, id, ownerID string, addons []string) (AddonList, error) {
	list, err := NormalizeClientAddons(addons)
	if err != nil {
		return nil, err
	}
	res := db.Model(&UserAPIKey{}).Where("id = ? AND user_id = ? AND kind = ?", id, ownerID, KeyKindClient).Update("addons", list)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return list, nil
}

// AllowsModel reports whether a client key may use the model. It needs the
// client_inference feature and the name in the list of the key. An empty list
// reaches nothing, and so does a request that names no model.
func (k *UserAPIKey) AllowsModel(model string) bool {
	return model != "" && k.ClientAllows(FeatureClientInference) && slices.Contains(k.Models, model)
}

// SetClientKeyModels replaces the model list of a client key of the owner. The
// key must hold client_inference, which needs at least one model, and any other
// key takes no list. Each model is checked with check (nil skips the check).
func SetClientKeyModels(db *gorm.DB, id, ownerID string, models []string, check ClientModelCheck) (ModelList, error) {
	list, err := NormalizeClientModels(models)
	if err != nil {
		return nil, err
	}
	var k UserAPIKey
	if err := db.Where("id = ? AND user_id = ? AND kind = ?", id, ownerID, KeyKindClient).First(&k).Error; err != nil {
		return nil, err
	}
	if k.Features[FeatureClientInference] != (len(list) > 0) {
		return nil, ErrClientModels
	}
	if err := checkClientModels(list, check); err != nil {
		return nil, err
	}
	res := db.Model(&UserAPIKey{}).Where("id = ? AND user_id = ? AND kind = ?", id, ownerID, KeyKindClient).Update("models", list)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return list, nil
}

// FolderList is a list of folder names stored as JSON text.
type FolderList []string

// Value implements driver.Valuer.
func (l FolderList) Value() (driver.Value, error) { return listValue(l) }

// Scan implements sql.Scanner.
func (l *FolderList) Scan(value any) error { return listScan((*[]string)(l), value) }

// Limits of a folder grant.
const (
	// MaxClientReadFolders bounds the folders a key may read.
	MaxClientReadFolders = 8
	// MaxClientFolderBytes and MaxClientFolderSegments bound one folder name.
	MaxClientFolderBytes    = 255
	MaxClientFolderSegments = 8
)

// ErrClientFolders is the refusal of a folder grant that does not fit the
// features: client_folders needs at least one folder, and a folder without
// that feature has no use.
var ErrClientFolders error = &clientInputError{msg: "client_folders needs a read folder or a write folder, and only client_folders takes them"}

// NormalizeClientFolder validates one folder of a grant and returns it in its
// clean form. The folder is relative to the workspace of the owner: it is not
// an absolute path, has no parent reference or dot segment, no empty segment
// (so no trailing slash), no NUL or other control character and no backslash.
// It names at least one folder, so a grant never covers the whole workspace.
func NormalizeClientFolder(f string) (string, error) {
	bad := func() (string, error) {
		return "", invalidClientInput("invalid folder %q", utils.TruncateUTF8(f, 64))
	}
	if f == "" || len(f) > MaxClientFolderBytes || strings.HasPrefix(f, "/") || strings.ContainsRune(f, '\\') || !utf8.ValidString(f) || strings.ContainsFunc(f, unicode.IsControl) {
		return bad()
	}
	segs := strings.Split(f, "/")
	if len(segs) > MaxClientFolderSegments {
		return bad()
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || strings.TrimSpace(s) != s {
			return bad()
		}
	}
	// The workspace has its own rules for a name; a folder it would refuse
	// could never be reached.
	if _, err := workspace.SplitPath("/" + f); err != nil {
		return bad()
	}
	return f, nil
}

// NormalizeClientFolders validates the folder grant of a code or a key: up to
// MaxClientReadFolders read folders (duplicates removed, sorted) and at most
// one write folder. A key that may write a folder may also read it, so the
// write folder is not repeated in the read list by the caller.
func NormalizeClientFolders(read []string, write string) (FolderList, string, error) {
	seen := map[string]bool{}
	out := FolderList{}
	for _, f := range read {
		n, err := NormalizeClientFolder(f)
		if err != nil {
			return nil, "", err
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > MaxClientReadFolders {
		return nil, "", invalidClientInput("at most %d read folders", MaxClientReadFolders)
	}
	slices.Sort(out)
	if write != "" {
		w, err := NormalizeClientFolder(write)
		if err != nil {
			return nil, "", err
		}
		write = w
	}
	return out, write, nil
}

// SetClientKeyFolders replaces the folder grant of a client key of the owner.
// Empty lists are allowed and give the key no access. A key without
// client_folders takes only empty lists.
func SetClientKeyFolders(db *gorm.DB, id, ownerID string, read []string, write string) (FolderList, string, error) {
	list, w, err := NormalizeClientFolders(read, write)
	if err != nil {
		return nil, "", err
	}
	var k UserAPIKey
	if err := db.Where("id = ? AND user_id = ? AND kind = ?", id, ownerID, KeyKindClient).First(&k).Error; err != nil {
		return nil, "", err
	}
	if !k.Features[FeatureClientFolders] && (len(list) > 0 || w != "") {
		return nil, "", ErrClientFolders
	}
	res := db.Model(&UserAPIKey{}).Where("id = ? AND user_id = ? AND kind = ?", id, ownerID, KeyKindClient).
		Updates(map[string]any{"read_folders": list, "write_folder": w})
	if res.Error != nil {
		return nil, "", res.Error
	}
	if res.RowsAffected == 0 {
		return nil, "", gorm.ErrRecordNotFound
	}
	return list, w, nil
}

// ErrClientFolderDenied is the refusal of ClientFolderGrant. Its text is for the
// log of the host: a caller turns it into one generic refusal.
var ErrClientFolderDenied = errors.New("client folder access denied")

// ClientFolderGrant returns the folder grant of a client key for an add-on. The
// key must stand now (it exists, is not expired or paused, its owner is an
// active administrator), hold client_folders, and be allowed to reach the
// add-on. Anything else is ErrClientFolderDenied. A key without a grant gets an
// empty one, which reaches nothing. It reads the key from the store on every
// call, so a change of the grant or a revoke applies to the next operation.
func ClientFolderGrant(ctx context.Context, db *gorm.DB, keyID, ownerID, addonID string, now time.Time) (read []string, write string, err error) {
	if db == nil || keyID == "" || ownerID == "" || addonID == "" {
		return nil, "", fmt.Errorf("%w: no client", ErrClientFolderDenied)
	}
	var k UserAPIKey
	if e := db.WithContext(ctx).Preload("User").Where("id = ? AND user_id = ? AND kind = ?", keyID, ownerID, KeyKindClient).First(&k).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil, "", fmt.Errorf("%w: the client is not paired", ErrClientFolderDenied)
		}
		return nil, "", e
	}
	switch {
	case k.CheckUsable(now) != nil:
		return nil, "", fmt.Errorf("%w: the client key is expired, paused or its owner is not an active administrator", ErrClientFolderDenied)
	case !k.AllowsAddon(addonID):
		return nil, "", fmt.Errorf("%w: the client may not reach this add-on", ErrClientFolderDenied)
	case !k.ClientAllows(FeatureClientFolders):
		return nil, "", fmt.Errorf("%w: the client has no folder grant", ErrClientFolderDenied)
	}
	return slices.Clone(k.ReadFolders), k.WriteFolder, nil
}
