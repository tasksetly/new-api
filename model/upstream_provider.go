package model

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	UpstreamProviderTypeSub2API = "sub2api"
	UpstreamProviderTypeCodeGo  = "codego"
	UpstreamProviderTypeNewAPI  = "newapi"
)

var ErrUpstreamProviderConfigurationChanged = errors.New("upstream provider configuration changed")

// UpstreamProvider stores a remote CodeGo-Api, NewAPI, or Sub2API account. Credential
// fields deliberately have no JSON representation, so management responses
// cannot disclose reusable upstream credentials.
type UpstreamProvider struct {
	Id                    int        `json:"id"`
	Name                  string     `json:"name" gorm:"type:varchar(128);not null;uniqueIndex"`
	Type                  string     `json:"type" gorm:"type:varchar(32);not null;index"`
	BaseURL               string     `json:"base_url" gorm:"type:varchar(512);not null"`
	Username              string     `json:"username" gorm:"type:varchar(255)"`
	PasswordEncrypted     string     `json:"-" gorm:"type:text"`
	TokenEncrypted        string     `json:"-" gorm:"type:text"`
	RefreshTokenEncrypted string     `json:"-" gorm:"type:text"`
	TotpSecretEncrypted   string     `json:"-" gorm:"type:text"`
	TokenExpiresAt        *time.Time `json:"token_expires_at,omitempty"`
	ConfigVersion         int64      `json:"-" gorm:"bigint;not null;default:1"`
	Balance               *float64   `json:"balance,omitempty"`
	FrozenBalance         *float64   `json:"frozen_balance,omitempty"`
	// UpstreamCost30Days is the actual cost reported by the upstream for its
	// rolling 30-day usage window. It is kept separate from local relay logs so
	// local pricing, group overrides, and later rate changes cannot rewrite the
	// upstream's reported cost.
	UpstreamCost30Days  *float64   `json:"upstream_cost_30d,omitempty" gorm:"column:upstream_cost_30d"`
	UpstreamCostAt      *time.Time `json:"upstream_cost_at,omitempty"`
	UpstreamConcurrency *int       `json:"upstream_concurrency,omitempty"`
	UpstreamUserID      string     `json:"upstream_user_id,omitempty" gorm:"type:varchar(128);index"`
	Status              int        `json:"status" gorm:"index"`
	LastSyncAt          *time.Time `json:"last_sync_at,omitempty"`
	LastSyncError       string     `json:"last_sync_error,omitempty" gorm:"type:text"`
	RateCorrection      float64    `json:"rate_correction"`
	SyncEnabled         bool       `json:"sync_enabled"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (UpstreamProvider) TableName() string {
	return "upstream_providers"
}

// UpstreamGroup is a provider-owned remote group and its effective upstream
// rate. Remote group identifiers are strings because CodeGo-Api and NewAPI group names are
// identifiers while Sub2API uses numeric identifiers.
type UpstreamGroup struct {
	Id                      int       `json:"id"`
	UpstreamProviderID      int       `json:"upstream_provider_id" gorm:"not null;index;uniqueIndex:idx_upstream_group_provider_remote,priority:1"`
	RemoteGroupID           string    `json:"remote_group_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_upstream_group_provider_remote,priority:2"`
	Name                    string    `json:"name" gorm:"type:varchar(128);not null"`
	Description             string    `json:"description,omitempty" gorm:"type:text"`
	Platform                string    `json:"platform,omitempty" gorm:"type:varchar(128)"`
	Models                  string    `json:"models,omitempty" gorm:"type:text"`
	RateMultiplier          float64   `json:"rate_multiplier"`
	EffectiveRateMultiplier *float64  `json:"effective_rate_multiplier,omitempty"`
	SuccessRate             *float64  `json:"success_rate,omitempty"`
	RequestCount            int64     `json:"request_count"`
	IsDynamic               bool      `json:"is_dynamic"`
	SyncedAt                time.Time `json:"synced_at"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func (UpstreamGroup) TableName() string {
	return "upstream_groups"
}

func CreateUpstreamProvider(provider *UpstreamProvider) error {
	if provider == nil {
		return errors.New("upstream provider is required")
	}
	if err := normalizeUpstreamProvider(provider); err != nil {
		return err
	}
	if provider.ConfigVersion <= 0 {
		provider.ConfigVersion = 1
	}
	provider.Id = 0
	return DB.Create(provider).Error
}

func UpdateUpstreamProvider(provider *UpstreamProvider) error {
	if provider == nil || provider.Id <= 0 {
		return errors.New("upstream provider ID is required")
	}
	if err := normalizeUpstreamProvider(provider); err != nil {
		return err
	}
	var existing UpstreamProvider
	if err := DB.Select("id", "config_version").First(&existing, provider.Id).Error; err != nil {
		return err
	}
	provider.ConfigVersion = max(existing.ConfigVersion, provider.ConfigVersion) + 1
	return DB.Save(provider).Error
}

func GetUpstreamProviderById(id int) (*UpstreamProvider, error) {
	var provider UpstreamProvider
	if err := DB.First(&provider, id).Error; err != nil {
		return nil, err
	}
	return &provider, nil
}

// GetUpstreamProviderByIdForUpdate locks a provider for a short management
// transaction. It serializes local binding, deletion, and endpoint changes so
// a channel cannot be left pointing at an obsolete provider record.
func GetUpstreamProviderByIdForUpdate(tx *gorm.DB, id int) (*UpstreamProvider, error) {
	if tx == nil {
		return nil, errors.New("database transaction is required")
	}
	var provider UpstreamProvider
	if err := lockForUpdate(tx).First(&provider, id).Error; err != nil {
		return nil, err
	}
	return &provider, nil
}

func ListUpstreamProviders(offset, limit int) ([]*UpstreamProvider, int64, error) {
	query := DB.Model(&UpstreamProvider{})
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var providers []*UpstreamProvider
	if limit > 0 {
		query = query.Offset(max(offset, 0)).Limit(limit)
	}
	if err := query.Order("id DESC").Find(&providers).Error; err != nil {
		return nil, 0, err
	}
	return providers, total, nil
}

func ListUpstreamGroups(upstreamProviderID int) ([]*UpstreamGroup, error) {
	var groups []*UpstreamGroup
	if err := DB.Where("upstream_provider_id = ?", upstreamProviderID).
		Order("name ASC").
		Find(&groups).Error; err != nil {
		return nil, err
	}
	return groups, nil
}

func GetUpstreamGroupByRemoteID(upstreamProviderID int, remoteGroupID string) (*UpstreamGroup, error) {
	var group UpstreamGroup
	err := DB.Where("upstream_provider_id = ? AND remote_group_id = ?", upstreamProviderID, remoteGroupID).
		First(&group).Error
	if err != nil {
		return nil, err
	}
	return &group, nil
}

func GetUpstreamGroupByRemoteIDForUpdate(tx *gorm.DB, upstreamProviderID int, remoteGroupID string) (*UpstreamGroup, error) {
	if tx == nil {
		return nil, errors.New("database transaction is required")
	}
	var group UpstreamGroup
	err := lockForUpdate(tx).Where("upstream_provider_id = ? AND remote_group_id = ?", upstreamProviderID, remoteGroupID).
		First(&group).Error
	if err != nil {
		return nil, err
	}
	return &group, nil
}

// ReplaceUpstreamGroups makes a successful remote group snapshot authoritative.
// It upserts current groups and removes groups no longer reported by that
// provider in one transaction.
func ReplaceUpstreamGroups(upstreamProviderID int, groups []UpstreamGroup) error {
	return replaceUpstreamGroups(upstreamProviderID, groups, nil)
}

// ReplaceUpstreamGroupsIfVersion applies a remote snapshot only when the
// provider configuration is still the version used to fetch it. This prevents
// a slow request to an old endpoint from overwriting a newly configured
// provider.
func ReplaceUpstreamGroupsIfVersion(upstreamProviderID int, configVersion int64, groups []UpstreamGroup) error {
	return replaceUpstreamGroups(upstreamProviderID, groups, &configVersion)
}

func replaceUpstreamGroups(upstreamProviderID int, groups []UpstreamGroup, expectedConfigVersion *int64) error {
	if upstreamProviderID <= 0 {
		return errors.New("upstream provider ID is required")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var provider UpstreamProvider
		if err := lockForUpdate(tx).First(&provider, upstreamProviderID).Error; err != nil {
			return err
		}
		if expectedConfigVersion != nil && provider.ConfigVersion != *expectedConfigVersion {
			return ErrUpstreamProviderConfigurationChanged
		}

		now := time.Now()
		remoteGroupIDs := make([]string, 0, len(groups))
		seenRemoteGroupIDs := make(map[string]struct{}, len(groups))
		for i := range groups {
			group := &groups[i]
			group.Id = 0
			group.UpstreamProviderID = upstreamProviderID
			group.RemoteGroupID = strings.TrimSpace(group.RemoteGroupID)
			if group.RemoteGroupID == "" {
				return errors.New("upstream group remote ID is required")
			}
			if _, exists := seenRemoteGroupIDs[group.RemoteGroupID]; exists {
				return errors.New("duplicate upstream group remote ID")
			}
			seenRemoteGroupIDs[group.RemoteGroupID] = struct{}{}
			remoteGroupIDs = append(remoteGroupIDs, group.RemoteGroupID)
			if group.SyncedAt.IsZero() {
				group.SyncedAt = now
			}
			group.UpdatedAt = now
		}

		if len(groups) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "upstream_provider_id"},
					{Name: "remote_group_id"},
				},
				DoUpdates: clause.AssignmentColumns([]string{
					"name",
					"description",
					"rate_multiplier",
					"effective_rate_multiplier",
					"is_dynamic",
					"synced_at",
					"updated_at",
				}),
			}).Create(&groups).Error; err != nil {
				return err
			}
		}

		query := tx.Where("upstream_provider_id = ?", upstreamProviderID)
		if len(remoteGroupIDs) > 0 {
			query = query.Where("remote_group_id NOT IN ?", remoteGroupIDs)
		}
		if err := query.Delete(&UpstreamGroup{}).Error; err != nil {
			return err
		}

		// A remote group that disappeared or became dynamic must not keep
		// routing through an old local channel and an old cost multiplier.
		// Disable its channel and ability rows in the same transaction as the
		// authoritative group snapshot.
		staticRemoteGroupIDs := make([]string, 0, len(groups))
		for i := range groups {
			if !groups[i].IsDynamic {
				staticRemoteGroupIDs = append(staticRemoteGroupIDs, groups[i].RemoteGroupID)
			}
		}
		staleChannels := tx.Model(&Channel{}).Where("upstream_provider_id = ?", upstreamProviderID)
		if len(staticRemoteGroupIDs) > 0 {
			staleChannels = staleChannels.Where("upstream_group_id NOT IN ?", staticRemoteGroupIDs)
		}
		var staleChannelIDs []int
		if err := staleChannels.Pluck("id", &staleChannelIDs).Error; err != nil {
			return err
		}
		if len(staleChannelIDs) == 0 {
			return nil
		}
		if err := tx.Model(&Channel{}).Where("id IN ?", staleChannelIDs).
			Update("status", common.ChannelStatusManuallyDisabled).Error; err != nil {
			return err
		}
		return tx.Model(&Ability{}).Where("channel_id IN ?", staleChannelIDs).Update("enabled", false).Error
	})
}

// DeleteUpstreamProvider retains provisioned local channels but clears their
// remote-management metadata before removing the provider and group snapshot.
func DeleteUpstreamProvider(id int) error {
	if id <= 0 {
		return errors.New("upstream provider ID is required")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if _, err := GetUpstreamProviderByIdForUpdate(tx, id); err != nil {
			return err
		}
		result := tx.Model(&Channel{}).
			Where("upstream_provider_id = ?", id).
			Updates(map[string]any{
				"upstream_provider_id": nil,
				"upstream_remote_id":   nil,
				"upstream_group_id":    nil,
				"upstream_key_id":      nil,
				"upstream_rate":        nil,
				"upstream_cost_rate":   nil,
			})
		if result.Error != nil {
			return result.Error
		}
		if err := tx.Where("upstream_provider_id = ?", id).Delete(&UpstreamGroup{}).Error; err != nil {
			return err
		}
		result = tx.Delete(&UpstreamProvider{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func normalizeUpstreamProvider(provider *UpstreamProvider) error {
	provider.Name = strings.TrimSpace(provider.Name)
	provider.Type = strings.ToLower(strings.TrimSpace(provider.Type))
	provider.BaseURL = strings.TrimSpace(provider.BaseURL)
	provider.Username = strings.TrimSpace(provider.Username)
	provider.UpstreamUserID = strings.TrimSpace(provider.UpstreamUserID)
	if provider.Name == "" {
		return errors.New("upstream provider name is required")
	}
	if provider.Type == "" {
		return errors.New("upstream provider type is required")
	}
	if provider.BaseURL == "" {
		return errors.New("upstream provider base URL is required")
	}
	if provider.RateCorrection == 0 {
		provider.RateCorrection = 1
	}
	if provider.RateCorrection <= 0 || math.IsNaN(provider.RateCorrection) || math.IsInf(provider.RateCorrection, 0) {
		return errors.New("upstream provider rate correction must be positive and finite")
	}
	return nil
}
