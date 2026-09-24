package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

const upstreamProviderStatusActive = 1

var errUpstreamProviderConfigurationChanged = errors.New("upstream provider configuration changed while synchronization was in progress")

// UpstreamProviderMutation is the allowed management input. Pointer fields
// preserve the distinction between leaving an existing credential unchanged
// and explicitly clearing it.
type UpstreamProviderMutation struct {
	Name           *string
	Type           *string
	BaseURL        *string
	Username       *string
	Password       *string
	Token          *string
	RefreshToken   *string
	TOTPSecret     *string
	UpstreamUserID *string
	RateCorrection *float64
	SyncEnabled    *bool
}

type UpstreamProviderView struct {
	ID                  int      `json:"id"`
	Name                string   `json:"name"`
	Type                string   `json:"type"`
	BaseURL             string   `json:"base_url"`
	Username            string   `json:"username,omitempty"`
	UpstreamUserID      string   `json:"upstream_user_id,omitempty"`
	RateCorrection      float64  `json:"rate_correction"`
	Balance             *float64 `json:"balance,omitempty"`
	FrozenBalance       *float64 `json:"frozen_balance,omitempty"`
	UpstreamCost30Days  *float64 `json:"upstream_cost_30d,omitempty"`
	UpstreamCostAt      *int64   `json:"upstream_cost_at,omitempty"`
	UpstreamConcurrency *int     `json:"upstream_concurrency,omitempty"`
	Status              string   `json:"status"`
	LastSyncAt          *int64   `json:"last_sync_at,omitempty"`
	LastSyncError       string   `json:"last_sync_error,omitempty"`
	SyncEnabled         bool     `json:"sync_enabled"`
	CreatedAt           int64    `json:"created_at"`
	UpdatedAt           int64    `json:"updated_at"`
	ChannelCount        int64    `json:"channel_count"`
	GroupCount          int64    `json:"group_count"`
	HasPassword         bool     `json:"has_password"`
	HasToken            bool     `json:"has_token"`
	HasRefreshToken     bool     `json:"has_refresh_token"`
	HasTOTPSecret       bool     `json:"has_totp_secret"`
}

type UpstreamProviderGroupView struct {
	ID                      int      `json:"id"`
	RemoteGroupID           string   `json:"remote_group_id"`
	Name                    string   `json:"name"`
	Description             string   `json:"description,omitempty"`
	Platform                string   `json:"platform,omitempty"`
	Models                  []string `json:"models,omitempty"`
	RateMultiplier          *float64 `json:"rate_multiplier,omitempty"`
	EffectiveRateMultiplier *float64 `json:"effective_rate_multiplier,omitempty"`
	SuccessRate             *float64 `json:"success_rate,omitempty"`
	RequestCount            int64    `json:"request_count"`
	IsDynamic               bool     `json:"is_dynamic"`
	CorrectedRate           *float64 `json:"corrected_rate,omitempty"`
	ChannelCount            int64    `json:"channel_count"`
	LastSyncedAt            int64    `json:"last_synced_at"`
}

type UpstreamProviderPage struct {
	Items    []*UpstreamProviderView `json:"items"`
	PageInfo *common.PageInfo        `json:"page_info"`
}

type UpstreamProvisionInput struct {
	ProviderID     int
	RemoteGroupIDs []string
	LocalGroup     string
	NamePrefix     string
}

type UpstreamProvisionResult struct {
	RemoteGroupID   string `json:"remote_group_id"`
	RemoteGroupName string `json:"remote_group_name"`
	ChannelID       int    `json:"channel_id,omitempty"`
	ChannelName     string `json:"channel_name,omitempty"`
	Error           string `json:"error,omitempty"`
}

type UpstreamSyncResult struct {
	ProviderID int    `json:"provider_id"`
	Error      string `json:"error,omitempty"`
}

func CreateManagedUpstreamProvider(input UpstreamProviderMutation) (*UpstreamProviderView, error) {
	provider := &model.UpstreamProvider{Status: upstreamProviderStatusActive, RateCorrection: 1, SyncEnabled: true}
	if err := applyUpstreamProviderMutation(provider, input, true); err != nil {
		return nil, err
	}
	if err := model.CreateUpstreamProvider(provider); err != nil {
		return nil, err
	}
	return upstreamProviderView(provider)
}

func UpdateManagedUpstreamProvider(id int, input UpstreamProviderMutation) (*UpstreamProviderView, error) {
	var updatedProvider *model.UpstreamProvider
	updatedChannels := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		provider, err := model.GetUpstreamProviderByIdForUpdate(tx, id)
		if err != nil {
			return err
		}
		previousType := provider.Type
		previousBaseURL := provider.BaseURL
		previousRateCorrection := provider.RateCorrection
		previousUsername := provider.Username
		previousPassword := provider.PasswordEncrypted
		previousToken := provider.TokenEncrypted
		previousRefreshToken := provider.RefreshTokenEncrypted
		previousTOTPSecret := provider.TotpSecretEncrypted
		previousRemoteUserID := provider.UpstreamUserID
		if err := applyUpstreamProviderMutation(provider, input, false); err != nil {
			return err
		}

		endpointChanged := provider.Type != previousType || provider.BaseURL != previousBaseURL
		sessionConfigurationChanged := endpointChanged ||
			provider.BaseURL != previousBaseURL ||
			provider.Username != previousUsername ||
			provider.PasswordEncrypted != previousPassword ||
			provider.TokenEncrypted != previousToken ||
			provider.RefreshTokenEncrypted != previousRefreshToken ||
			provider.TotpSecretEncrypted != previousTOTPSecret ||
			provider.UpstreamUserID != previousRemoteUserID
		configurationChanged := sessionConfigurationChanged || provider.RateCorrection != previousRateCorrection
		if endpointChanged {
			var boundChannelCount int64
			if err := tx.Model(&model.Channel{}).Where("upstream_provider_id = ?", provider.Id).Count(&boundChannelCount).Error; err != nil {
				return err
			}
			if boundChannelCount > 0 {
				return errors.New("cannot change an upstream URL or type while local channels are bound; create a new provider and re-provision its channels")
			}
			// A token issued by another host or another protocol must never be sent
			// to the newly configured upstream. Password and TOTP credentials are
			// also endpoint-scoped secrets and must be explicitly supplied again.
			if input.Username == nil {
				provider.Username = ""
			}
			if input.Password == nil {
				provider.PasswordEncrypted = ""
			}
			if input.TOTPSecret == nil {
				provider.TotpSecretEncrypted = ""
			}
			if input.Token == nil {
				provider.TokenEncrypted = ""
			}
			if input.RefreshToken == nil {
				provider.RefreshTokenEncrypted = ""
			}
			if input.UpstreamUserID == nil {
				provider.UpstreamUserID = ""
			}
			provider.TokenExpiresAt = nil
			if err := validateManagedUpstreamProviderCredentials(provider); err != nil {
				return err
			}
		}
		if configurationChanged {
			if provider.ConfigVersion <= 0 {
				provider.ConfigVersion = 1
			}
			provider.ConfigVersion++
		}
		if sessionConfigurationChanged {
			if err := tx.Where("upstream_provider_id = ?", provider.Id).Delete(&model.UpstreamGroup{}).Error; err != nil {
				return err
			}
		}

		if err := tx.Save(provider).Error; err != nil {
			return err
		}
		if provider.RateCorrection == previousRateCorrection {
			updatedProvider = provider
			return nil
		}

		var channels []model.Channel
		if err := tx.Select("id", "upstream_rate").Where("upstream_provider_id = ?", provider.Id).Find(&channels).Error; err != nil {
			return err
		}
		for i := range channels {
			channel := &channels[i]
			if channel.UpstreamRate == nil {
				continue
			}
			costRate := *channel.UpstreamRate * provider.RateCorrection
			if !upstreamValidMultiplier(costRate) {
				return errors.New("upstream group cost rate is invalid")
			}
			if err := tx.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("upstream_cost_rate", costRate).Error; err != nil {
				return err
			}
			updatedChannels = true
		}
		updatedProvider = provider
		return nil
	})
	if err != nil {
		return nil, err
	}
	if updatedChannels {
		model.InitChannelCache()
	}
	return upstreamProviderView(updatedProvider)
}

func GetManagedUpstreamProvider(id int) (*UpstreamProviderView, error) {
	provider, err := model.GetUpstreamProviderById(id)
	if err != nil {
		return nil, err
	}
	return upstreamProviderView(provider)
}

func ListManagedUpstreamProviders(pageInfo *common.PageInfo) (*UpstreamProviderPage, error) {
	if pageInfo == nil {
		pageInfo = &common.PageInfo{Page: 1, PageSize: common.ItemsPerPage}
	}
	providers, total, err := model.ListUpstreamProviders(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		return nil, err
	}
	items := make([]*UpstreamProviderView, 0, len(providers))
	for _, provider := range providers {
		view, err := upstreamProviderView(provider)
		if err != nil {
			return nil, err
		}
		items = append(items, view)
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	return &UpstreamProviderPage{Items: items, PageInfo: pageInfo}, nil
}

func DeleteManagedUpstreamProvider(id int) error {
	if err := model.DeleteUpstreamProvider(id); err != nil {
		return err
	}
	model.InitChannelCache()
	return nil
}

func TestManagedUpstreamProvider(ctx context.Context, id int) (*upstreamProviderRemoteProfile, error) {
	provider, err := model.GetUpstreamProviderById(id)
	if err != nil {
		return nil, err
	}
	client, session, credentials, err := managedUpstreamSession(ctx, provider)
	if err != nil {
		return nil, err
	}
	profile, err := client.Profile(ctx, session)
	if errors.Is(err, errUpstreamProviderUnauthorized) {
		session, err = renewManagedUpstreamSession(ctx, provider, client, credentials)
		if err == nil {
			updateManagedUpstreamCredentials(&credentials, session)
			profile, err = client.Profile(ctx, session)
		}
	}
	return profile, err
}

func SyncManagedUpstreamProvider(ctx context.Context, id int) (*UpstreamProviderView, error) {
	provider, err := model.GetUpstreamProviderById(id)
	if err != nil {
		return nil, err
	}
	if err := syncManagedUpstreamProvider(ctx, provider); err != nil {
		_ = markManagedUpstreamSyncFailed(provider, err)
		return nil, err
	}
	return upstreamProviderView(provider)
}

func SyncAllManagedUpstreamProviders(ctx context.Context) ([]UpstreamSyncResult, error) {
	var providers []*model.UpstreamProvider
	if err := model.DB.Where("sync_enabled = ? AND status = ?", true, upstreamProviderStatusActive).
		Order("id ASC").Find(&providers).Error; err != nil {
		return nil, err
	}
	results := make([]UpstreamSyncResult, 0, len(providers))
	for _, provider := range providers {
		result := UpstreamSyncResult{ProviderID: provider.Id}
		if err := syncManagedUpstreamProvider(ctx, provider); err != nil {
			_ = markManagedUpstreamSyncFailed(provider, err)
			result.Error = managedUpstreamError(err, provider)
		}
		results = append(results, result)
	}
	return results, nil
}

func ListManagedUpstreamGroups(id int) ([]*UpstreamProviderGroupView, error) {
	provider, err := model.GetUpstreamProviderById(id)
	if err != nil {
		return nil, err
	}
	groups, err := model.ListUpstreamGroups(id)
	if err != nil {
		return nil, err
	}
	items := make([]*UpstreamProviderGroupView, 0, len(groups))
	for _, group := range groups {
		view, err := upstreamProviderGroupView(provider, group)
		if err != nil {
			return nil, err
		}
		items = append(items, view)
	}
	return items, nil
}

func ProvisionManagedUpstreamChannels(ctx context.Context, input UpstreamProvisionInput) ([]UpstreamProvisionResult, error) {
	if input.ProviderID <= 0 {
		return nil, errors.New("upstream provider ID is required")
	}
	input.LocalGroup = strings.TrimSpace(input.LocalGroup)
	if input.LocalGroup == "" || !ratio_setting.ContainsGroupRatio(input.LocalGroup) {
		return nil, errors.New("a valid local channel group is required")
	}
	if len(input.RemoteGroupIDs) == 0 {
		return nil, errors.New("select at least one upstream group")
	}
	if len(input.RemoteGroupIDs) > 100 {
		return nil, errors.New("too many upstream groups selected")
	}
	if len(strings.TrimSpace(input.NamePrefix)) > 64 {
		return nil, errors.New("channel name prefix exceeds its maximum length")
	}
	provider, err := model.GetUpstreamProviderById(input.ProviderID)
	if err != nil {
		return nil, err
	}
	client, session, credentials, err := managedUpstreamSession(ctx, provider)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(input.RemoteGroupIDs))
	results := make([]UpstreamProvisionResult, 0, len(input.RemoteGroupIDs))
	createdChannel := false
	for _, remoteGroupID := range input.RemoteGroupIDs {
		remoteGroupID = strings.TrimSpace(remoteGroupID)
		if remoteGroupID == "" {
			continue
		}
		if len(remoteGroupID) > 128 {
			return nil, errors.New("upstream group ID exceeds its maximum length")
		}
		if _, exists := seen[remoteGroupID]; exists {
			continue
		}
		seen[remoteGroupID] = struct{}{}
		result := UpstreamProvisionResult{RemoteGroupID: remoteGroupID}
		group, err := model.GetUpstreamGroupByRemoteID(provider.Id, remoteGroupID)
		if err != nil {
			result.Error = "upstream group was not found; synchronize the provider first"
			results = append(results, result)
			continue
		}
		result.RemoteGroupName = group.Name
		if group.IsDynamic {
			result.Error = "dynamic upstream groups cannot be bound to a static local channel"
			results = append(results, result)
			continue
		}

		keyName, err := newUpstreamProvisionKeyName(provider.Id, input.NamePrefix)
		if err != nil {
			result.Error = "could not generate an upstream API key name"
			results = append(results, result)
			continue
		}
		remoteKey, err := client.CreateKey(ctx, session, keyName, group.RemoteGroupID)
		if errors.Is(err, errUpstreamProviderUnauthorized) {
			session, err = renewManagedUpstreamSession(ctx, provider, client, credentials)
			if err == nil {
				remoteKey, err = client.CreateKey(ctx, session, keyName, group.RemoteGroupID)
			}
		}
		if err != nil {
			result.Error = managedUpstreamError(err, provider)
			results = append(results, result)
			continue
		}
		models, err := client.Models(ctx, remoteKey.Key)
		if errors.Is(err, errUpstreamProviderUnauthorized) {
			session, err = renewManagedUpstreamSession(ctx, provider, client, credentials)
			if err == nil {
				models, err = client.Models(ctx, remoteKey.Key)
			}
		}
		if err != nil {
			cleanupManagedUpstreamKey(ctx, provider, client, session, credentials, remoteKey)
			result.Error = "could not bind the created upstream API key; cleanup was requested"
			results = append(results, result)
			continue
		}
		channel, err := createManagedUpstreamChannel(provider, group, remoteKey, models, input.LocalGroup, input.NamePrefix)
		if err != nil {
			cleanupManagedUpstreamKey(ctx, provider, client, session, credentials, remoteKey)
			result.Error = "could not bind the created upstream API key; cleanup was requested"
			results = append(results, result)
			continue
		}
		result.ChannelID = channel.Id
		result.ChannelName = channel.Name
		createdChannel = true
		results = append(results, result)
	}
	if len(results) == 0 {
		return nil, errors.New("select at least one upstream group")
	}
	if createdChannel {
		model.InitChannelCache()
	}
	return results, nil
}

func applyUpstreamProviderMutation(provider *model.UpstreamProvider, input UpstreamProviderMutation, creating bool) error {
	if provider == nil {
		return errors.New("upstream provider is required")
	}
	if input.Token != nil {
		normalizedToken := normalizeUpstreamBearerToken(*input.Token)
		input.Token = &normalizedToken
	}
	if input.Name != nil {
		provider.Name = strings.TrimSpace(*input.Name)
	}
	if input.Type != nil {
		provider.Type = strings.ToLower(strings.TrimSpace(*input.Type))
	}
	if input.BaseURL != nil {
		normalized, err := NormalizeUpstreamProviderBaseURL(*input.BaseURL)
		if err != nil {
			return err
		}
		provider.BaseURL = normalized
	}
	if input.Username != nil {
		provider.Username = strings.TrimSpace(*input.Username)
	}
	if input.UpstreamUserID != nil {
		provider.UpstreamUserID = strings.TrimSpace(*input.UpstreamUserID)
	}
	if input.RateCorrection != nil {
		if !upstreamValidRateCorrection(*input.RateCorrection) || *input.RateCorrection > 1_000_000 {
			return errors.New("rate correction must be positive and finite")
		}
		provider.RateCorrection = *input.RateCorrection
	}
	if input.SyncEnabled != nil {
		provider.SyncEnabled = *input.SyncEnabled
	}
	if len(provider.Name) > 128 || len(provider.BaseURL) > 512 || len(provider.Username) > 255 || len(provider.UpstreamUserID) > 128 {
		return errors.New("upstream provider field exceeds its maximum length")
	}
	for _, credential := range []struct {
		input *string
		field *string
	}{
		{input.Password, &provider.PasswordEncrypted},
		{input.Token, &provider.TokenEncrypted},
		{input.RefreshToken, &provider.RefreshTokenEncrypted},
		{input.TOTPSecret, &provider.TotpSecretEncrypted},
	} {
		if credential.input == nil {
			continue
		}
		if len(*credential.input) > 32*1024 {
			return errors.New("upstream credential exceeds its maximum length")
		}
		if *credential.input == "" {
			*credential.field = ""
			continue
		}
		ciphertext, err := common.EncryptUpstreamCredential(*credential.input)
		if err != nil {
			return errors.New("could not encrypt upstream credentials")
		}
		*credential.field = ciphertext
	}
	if provider.Type == model.UpstreamProviderTypeNewAPI && input.Password == nil && input.RefreshToken == nil && input.TOTPSecret == nil {
		// NewAPI only accepts the manually supplied management token.
		provider.Username = ""
		provider.PasswordEncrypted = ""
		provider.RefreshTokenEncrypted = ""
		provider.TotpSecretEncrypted = ""
	}
	if creating && (provider.Name == "" || provider.BaseURL == "") {
		return errors.New("provider name and upstream URL are required")
	}
	if err := validateManagedUpstreamProviderCredentials(provider); err != nil {
		return err
	}
	if provider.RateCorrection == 0 {
		provider.RateCorrection = 1
	}
	return nil
}

func validateManagedUpstreamProviderCredentials(provider *model.UpstreamProvider) error {
	if !validManagedUpstreamProviderType(provider.Type) {
		return errors.New("unsupported upstream provider type")
	}
	hasSessionCredential := provider.TokenEncrypted != "" ||
		(provider.Type == model.UpstreamProviderTypeSub2API && provider.RefreshTokenEncrypted != "")
	if provider.PasswordEncrypted == "" && !hasSessionCredential {
		return errors.New("upstream credentials, an access token, or a Sub2API refresh token are required")
	}
	if provider.PasswordEncrypted != "" && provider.Username == "" {
		return errors.New("upstream username is required when using a password")
	}
	if provider.Type == model.UpstreamProviderTypeCodeGo && provider.TokenEncrypted != "" && provider.UpstreamUserID == "" {
		return errors.New("CodeGo-Api access token requires the remote user ID")
	}
	if provider.Type == model.UpstreamProviderTypeNewAPI {
		if provider.TokenEncrypted == "" {
			return errors.New("NewAPI management access token is required")
		}
		if provider.PasswordEncrypted != "" || provider.RefreshTokenEncrypted != "" || provider.TotpSecretEncrypted != "" {
			return errors.New("NewAPI uses a manually supplied management access token; password, refresh token, and TOTP are not supported")
		}
	}
	return nil
}

func validManagedUpstreamProviderType(providerType string) bool {
	return providerType == model.UpstreamProviderTypeSub2API || providerType == model.UpstreamProviderTypeCodeGo || providerType == model.UpstreamProviderTypeNewAPI
}

func managedUpstreamClient(provider *model.UpstreamProvider) (upstreamProviderRemoteClient, error) {
	if provider == nil {
		return nil, errors.New("upstream provider is required")
	}
	switch provider.Type {
	case model.UpstreamProviderTypeSub2API:
		return newSub2APIUpstreamClient(provider.BaseURL)
	case model.UpstreamProviderTypeCodeGo:
		return newCodeGoUpstreamClient(provider.BaseURL)
	case model.UpstreamProviderTypeNewAPI:
		return newNewAPIUpstreamClient(provider.BaseURL)
	default:
		return nil, errors.New("unsupported upstream provider type")
	}
}

func managedUpstreamCredentials(provider *model.UpstreamProvider) (upstreamProviderCredentials, error) {
	credentials := upstreamProviderCredentials{RemoteUser: provider.UpstreamUserID, ExpiresAt: provider.TokenExpiresAt}
	var err error
	if credentials.Password, err = common.DecryptUpstreamCredential(provider.PasswordEncrypted); err != nil {
		return upstreamProviderCredentials{}, errors.New("saved upstream password cannot be decrypted")
	}
	if credentials.Token, err = common.DecryptUpstreamCredential(provider.TokenEncrypted); err != nil {
		return upstreamProviderCredentials{}, errors.New("saved upstream access token cannot be decrypted")
	}
	if credentials.Refresh, err = common.DecryptUpstreamCredential(provider.RefreshTokenEncrypted); err != nil {
		return upstreamProviderCredentials{}, errors.New("saved upstream refresh token cannot be decrypted")
	}
	if credentials.TOTPSecret, err = common.DecryptUpstreamCredential(provider.TotpSecretEncrypted); err != nil {
		return upstreamProviderCredentials{}, errors.New("saved upstream TOTP secret cannot be decrypted")
	}
	return credentials, nil
}

func managedUpstreamSession(ctx context.Context, provider *model.UpstreamProvider) (upstreamProviderRemoteClient, upstreamProviderRemoteSession, upstreamProviderCredentials, error) {
	client, err := managedUpstreamClient(provider)
	if err != nil {
		return nil, upstreamProviderRemoteSession{}, upstreamProviderCredentials{}, err
	}
	credentials, err := managedUpstreamCredentials(provider)
	if err != nil {
		return nil, upstreamProviderRemoteSession{}, upstreamProviderCredentials{}, err
	}
	if strings.TrimSpace(credentials.Token) != "" &&
		(provider.Type == model.UpstreamProviderTypeNewAPI || !managedUpstreamTokenExpired(credentials.ExpiresAt)) {
		return client, upstreamProviderRemoteSession{
			Token: credentials.Token, RefreshToken: credentials.Refresh, RemoteUserID: credentials.RemoteUser, ExpiresAt: credentials.ExpiresAt,
		}, credentials, nil
	}
	session, err := renewManagedUpstreamSession(ctx, provider, client, credentials)
	if err != nil {
		return nil, upstreamProviderRemoteSession{}, credentials, err
	}
	credentials.Token = session.Token
	credentials.Refresh = session.RefreshToken
	credentials.RemoteUser = session.RemoteUserID
	credentials.ExpiresAt = session.ExpiresAt
	return client, session, credentials, nil
}

func managedUpstreamTokenExpired(expiresAt *time.Time) bool {
	return expiresAt != nil && !expiresAt.After(time.Now().Add(upstreamProviderTokenSkew))
}

func updateManagedUpstreamCredentials(credentials *upstreamProviderCredentials, session upstreamProviderRemoteSession) {
	if credentials == nil {
		return
	}
	credentials.Token = session.Token
	credentials.Refresh = session.RefreshToken
	credentials.RemoteUser = session.RemoteUserID
	credentials.ExpiresAt = session.ExpiresAt
}

// renewManagedUpstreamSession gives a Sub2API refresh token the first chance
// to renew the current session, then falls back to the saved password. The
// same flow is also used after an upstream rejects a hand-entered token whose
// expiration time was not available locally.
func renewManagedUpstreamSession(
	ctx context.Context,
	provider *model.UpstreamProvider,
	client upstreamProviderRemoteClient,
	credentials upstreamProviderCredentials,
) (upstreamProviderRemoteSession, error) {
	if provider != nil && (provider.Type == model.UpstreamProviderTypeCodeGo || provider.Type == model.UpstreamProviderTypeNewAPI) {
		if strings.TrimSpace(credentials.Token) != "" {
			return upstreamProviderRemoteSession{}, errUpstreamProviderManualToken
		}
		return upstreamProviderRemoteSession{}, errors.New("upstream management access token is missing")
	}
	var refreshErr error
	if provider.Type == model.UpstreamProviderTypeSub2API && strings.TrimSpace(credentials.Refresh) != "" {
		session, err := client.Refresh(ctx, credentials)
		if err == nil {
			if session.RemoteUserID == "" {
				session.RemoteUserID = credentials.RemoteUser
			}
			if err := persistManagedUpstreamSession(provider, session); err != nil {
				return upstreamProviderRemoteSession{}, err
			}
			return *session, nil
		}
		refreshErr = err
	}
	if strings.TrimSpace(credentials.Password) == "" {
		if refreshErr != nil {
			return upstreamProviderRemoteSession{}, refreshErr
		}
		if strings.TrimSpace(credentials.Token) != "" {
			return upstreamProviderRemoteSession{}, errors.New("upstream access token has expired")
		}
		return upstreamProviderRemoteSession{}, errors.New("upstream credentials are missing")
	}
	session, err := client.Login(ctx, credentials)
	if err != nil {
		return upstreamProviderRemoteSession{}, err
	}
	if session.RemoteUserID == "" {
		session.RemoteUserID = credentials.RemoteUser
	}
	if err := persistManagedUpstreamSession(provider, session); err != nil {
		return upstreamProviderRemoteSession{}, err
	}
	return *session, nil
}

func persistManagedUpstreamSession(provider *model.UpstreamProvider, session *upstreamProviderRemoteSession) error {
	if provider == nil || session == nil || strings.TrimSpace(session.Token) == "" {
		return errors.New("invalid upstream session")
	}
	token, err := common.EncryptUpstreamCredential(session.Token)
	if err != nil {
		return errors.New("could not encrypt upstream access token")
	}
	refresh := provider.RefreshTokenEncrypted
	if session.RefreshToken != "" {
		refresh, err = common.EncryptUpstreamCredential(session.RefreshToken)
		if err != nil {
			return errors.New("could not encrypt upstream refresh token")
		}
	}
	updates := map[string]any{
		"token_encrypted":         token,
		"refresh_token_encrypted": refresh,
		"token_expires_at":        session.ExpiresAt,
	}
	if session.RemoteUserID != "" {
		updates["upstream_user_id"] = session.RemoteUserID
		provider.UpstreamUserID = session.RemoteUserID
	}
	result := model.DB.Model(&model.UpstreamProvider{}).
		Where("id = ? AND config_version = ?", provider.Id, provider.ConfigVersion).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errUpstreamProviderConfigurationChanged
	}
	provider.TokenEncrypted = token
	provider.RefreshTokenEncrypted = refresh
	provider.TokenExpiresAt = session.ExpiresAt
	return nil
}

func syncManagedUpstreamProvider(ctx context.Context, provider *model.UpstreamProvider) error {
	client, session, credentials, err := managedUpstreamSession(ctx, provider)
	if err != nil {
		return err
	}
	profile, err := client.Profile(ctx, session)
	if errors.Is(err, errUpstreamProviderUnauthorized) {
		session, err = renewManagedUpstreamSession(ctx, provider, client, credentials)
		if err == nil {
			updateManagedUpstreamCredentials(&credentials, session)
			profile, err = client.Profile(ctx, session)
		}
	}
	if err != nil {
		return err
	}
	groups, err := client.Groups(ctx, session)
	if errors.Is(err, errUpstreamProviderUnauthorized) {
		session, err = renewManagedUpstreamSession(ctx, provider, client, credentials)
		if err == nil {
			updateManagedUpstreamCredentials(&credentials, session)
			groups, err = client.Groups(ctx, session)
		}
	}
	if err != nil {
		return err
	}
	usage, usageErr := client.Usage(ctx, session)
	if errors.Is(usageErr, errUpstreamProviderUnauthorized) {
		session, usageErr = renewManagedUpstreamSession(ctx, provider, client, credentials)
		if usageErr == nil {
			updateManagedUpstreamCredentials(&credentials, session)
			usage, usageErr = client.Usage(ctx, session)
		}
	}
	if errors.Is(usageErr, errUpstreamProviderConfigurationChanged) {
		return usageErr
	}
	now := time.Now()
	mirroredGroups := make([]model.UpstreamGroup, 0, len(groups))
	for _, group := range groups {
		if group.RemoteID == "" {
			continue
		}
		rate := group.Rate
		if !group.IsDynamic && !upstreamValidMultiplier(rate) {
			continue
		}
		mirroredGroups = append(mirroredGroups, model.UpstreamGroup{
			RemoteGroupID:           group.RemoteID,
			Name:                    group.Name,
			Description:             group.Description,
			Platform:                group.Platform,
			Models:                  marshalUpstreamGroupModels(group.Models),
			RateMultiplier:          rate,
			EffectiveRateMultiplier: group.EffectiveRate,
			SuccessRate:             group.SuccessRate,
			RequestCount:            group.RequestCount,
			IsDynamic:               group.IsDynamic,
			SyncedAt:                now,
		})
	}
	if err := model.ReplaceUpstreamGroupsIfVersion(provider.Id, provider.ConfigVersion, mirroredGroups); err != nil {
		if errors.Is(err, model.ErrUpstreamProviderConfigurationChanged) {
			return errUpstreamProviderConfigurationChanged
		}
		return err
	}
	// ReplaceUpstreamGroups can disable local channels whose remote group was
	// removed or became dynamic. Refresh the selection cache before the next
	// relay sees the authoritative snapshot.
	model.InitChannelCache()
	updates := map[string]any{
		"balance":              profile.Balance,
		"frozen_balance":       profile.Frozen,
		"upstream_concurrency": profile.Concurrency,
		"last_sync_at":         now,
		"last_sync_error":      "",
	}
	if usageErr != nil {
		// A cost endpoint is informational and may not exist on an older
		// compatible upstream. Preserve the successful balance and group
		// snapshot, keep the last known cost, and make the stale cost visible.
		updates["last_sync_error"] = "upstream cost was not synchronized"
	} else if usage != nil && usage.Cost30Days != nil {
		updates["upstream_cost_30d"] = *usage.Cost30Days
		updates["upstream_cost_at"] = now
	}
	if profile.RemoteUserID != "" {
		updates["upstream_user_id"] = profile.RemoteUserID
		provider.UpstreamUserID = profile.RemoteUserID
	}
	result := model.DB.Model(&model.UpstreamProvider{}).
		Where("id = ? AND config_version = ?", provider.Id, provider.ConfigVersion).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errUpstreamProviderConfigurationChanged
	}
	provider.Balance = profile.Balance
	provider.FrozenBalance = profile.Frozen
	provider.UpstreamConcurrency = profile.Concurrency
	provider.LastSyncAt = &now
	provider.LastSyncError = ""
	if usageErr != nil {
		provider.LastSyncError = "upstream cost was not synchronized"
	} else if usage != nil && usage.Cost30Days != nil {
		provider.UpstreamCost30Days = usage.Cost30Days
		provider.UpstreamCostAt = &now
	}
	if err := syncManagedUpstreamChannels(provider, mirroredGroups, profile.Balance, now); err != nil {
		return err
	}
	return nil
}

func syncManagedUpstreamChannels(provider *model.UpstreamProvider, groups []model.UpstreamGroup, balance *float64, syncedAt time.Time) error {
	updated := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		lockedProvider, err := model.GetUpstreamProviderByIdForUpdate(tx, provider.Id)
		if err != nil {
			return err
		}
		if lockedProvider.ConfigVersion != provider.ConfigVersion {
			return errUpstreamProviderConfigurationChanged
		}
		for i := range groups {
			group := &groups[i]
			if group.IsDynamic {
				continue
			}
			rate := group.RateMultiplier
			if group.EffectiveRateMultiplier != nil {
				rate = *group.EffectiveRateMultiplier
			}
			if !upstreamValidMultiplier(rate) {
				continue
			}
			costRate := rate * lockedProvider.RateCorrection
			if !upstreamValidMultiplier(costRate) || costRate > math.MaxFloat64/2 {
				return errors.New("upstream group cost rate is invalid")
			}
			updates := map[string]any{
				"upstream_rate":      rate,
				"upstream_cost_rate": costRate,
			}
			if balance != nil {
				updates["balance"] = *balance
				updates["balance_updated_time"] = syncedAt.Unix()
			}
			result := tx.Model(&model.Channel{}).
				Where("upstream_provider_id = ? AND upstream_group_id = ?", provider.Id, group.RemoteGroupID).
				Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			updated = updated || result.RowsAffected > 0
		}
		return nil
	})
	if err != nil {
		return err
	}
	if updated {
		model.InitChannelCache()
	}
	return nil
}

func markManagedUpstreamSyncFailed(provider *model.UpstreamProvider, err error) error {
	message := managedUpstreamError(err, provider)
	provider.LastSyncError = message
	return model.DB.Model(&model.UpstreamProvider{}).
		Where("id = ? AND config_version = ?", provider.Id, provider.ConfigVersion).
		Updates(map[string]any{
			"last_sync_error": message,
		}).Error
}

func managedUpstreamError(err error, _ *model.UpstreamProvider) string {
	return PublicManagedUpstreamError(err)
}

// PublicManagedUpstreamError deliberately exposes only stable, operator-safe
// error classes. Remote response text can contain credentials reflected by a
// compromised upstream and must never reach a management response or audit
// record.
func PublicManagedUpstreamError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, errUpstreamProviderUnauthorized):
		return "upstream rejected the credentials"
	case errors.Is(err, errUpstreamProviderManualToken):
		return "upstream management access token expired or was rejected; replace it manually"
	case err != nil && strings.Contains(err.Error(), "management access token is missing"):
		return "upstream management access token is missing"
	case errors.Is(err, errUpstreamProviderTOTPRequired):
		return "upstream requires two-factor authentication"
	case errors.Is(err, errUpstreamProviderRejected):
		return "upstream rejected the request"
	case errors.Is(err, errUpstreamProviderConfigurationChanged), errors.Is(err, model.ErrUpstreamProviderConfigurationChanged):
		return "upstream provider configuration changed; try again"
	}
	var statusError *upstreamProviderHTTPStatusError
	if errors.As(err, &statusError) {
		return "upstream request failed"
	}
	return "upstream operation failed"
}

func cleanupManagedUpstreamKey(
	ctx context.Context,
	provider *model.UpstreamProvider,
	client upstreamProviderRemoteClient,
	session upstreamProviderRemoteSession,
	credentials upstreamProviderCredentials,
	remoteKey *upstreamProviderRemoteKey,
) {
	if provider == nil || client == nil || remoteKey == nil || strings.TrimSpace(remoteKey.ID) == "" {
		return
	}
	err := client.DeleteKey(ctx, session, remoteKey.ID)
	if errors.Is(err, errUpstreamProviderUnauthorized) {
		if renewedSession, renewErr := renewManagedUpstreamSession(ctx, provider, client, credentials); renewErr == nil {
			err = client.DeleteKey(ctx, renewedSession, remoteKey.ID)
		}
	}
	if err != nil {
		common.SysError(fmt.Sprintf("failed to remove an unbound upstream API key for provider %d", provider.Id))
	}
}

func upstreamProviderView(provider *model.UpstreamProvider) (*UpstreamProviderView, error) {
	if provider == nil {
		return nil, errors.New("upstream provider is required")
	}
	var channelCount, groupCount int64
	if err := model.DB.Model(&model.Channel{}).Where("upstream_provider_id = ?", provider.Id).Count(&channelCount).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Model(&model.UpstreamGroup{}).Where("upstream_provider_id = ?", provider.Id).Count(&groupCount).Error; err != nil {
		return nil, err
	}
	view := &UpstreamProviderView{
		ID: provider.Id, Name: provider.Name, Type: provider.Type, BaseURL: provider.BaseURL, Username: provider.Username,
		UpstreamUserID: provider.UpstreamUserID, RateCorrection: provider.RateCorrection, Balance: provider.Balance,
		FrozenBalance: provider.FrozenBalance, UpstreamCost30Days: provider.UpstreamCost30Days, UpstreamConcurrency: provider.UpstreamConcurrency,
		Status: managedUpstreamProviderStatus(provider.Status), LastSyncError: provider.LastSyncError,
		SyncEnabled: provider.SyncEnabled, CreatedAt: provider.CreatedAt.Unix(), UpdatedAt: provider.UpdatedAt.Unix(),
		ChannelCount: channelCount, GroupCount: groupCount,
		HasPassword: provider.PasswordEncrypted != "", HasToken: provider.TokenEncrypted != "",
		HasRefreshToken: provider.RefreshTokenEncrypted != "", HasTOTPSecret: provider.TotpSecretEncrypted != "",
	}
	if provider.LastSyncAt != nil {
		lastSyncAt := provider.LastSyncAt.Unix()
		view.LastSyncAt = &lastSyncAt
	}
	if provider.UpstreamCostAt != nil {
		upstreamCostAt := provider.UpstreamCostAt.Unix()
		view.UpstreamCostAt = &upstreamCostAt
	}
	return view, nil
}

func upstreamProviderGroupView(provider *model.UpstreamProvider, group *model.UpstreamGroup) (*UpstreamProviderGroupView, error) {
	if provider == nil || group == nil {
		return nil, errors.New("upstream provider group is required")
	}
	channelCount, err := managedUpstreamChannelCount(provider.Id, group.RemoteGroupID)
	if err != nil {
		return nil, err
	}
	view := &UpstreamProviderGroupView{
		ID: group.Id, RemoteGroupID: group.RemoteGroupID, Name: group.Name, Description: group.Description,
		Platform: group.Platform, Models: unmarshalUpstreamGroupModels(group.Models), SuccessRate: group.SuccessRate, RequestCount: group.RequestCount,
		EffectiveRateMultiplier: group.EffectiveRateMultiplier, IsDynamic: group.IsDynamic,
		ChannelCount: channelCount, LastSyncedAt: group.SyncedAt.Unix(),
	}
	if !group.IsDynamic && upstreamValidMultiplier(group.RateMultiplier) {
		rate := group.RateMultiplier
		view.RateMultiplier = &rate
		effective := rate
		if group.EffectiveRateMultiplier != nil {
			effective = *group.EffectiveRateMultiplier
		}
		corrected := effective * provider.RateCorrection
		if upstreamValidMultiplier(corrected) {
			view.CorrectedRate = &corrected
		}
	}
	return view, nil
}

func marshalUpstreamGroupModels(models []string) string {
	if len(models) == 0 {
		return ""
	}
	encoded, err := common.Marshal(models)
	if err != nil {
		common.SysError("failed to marshal upstream group models: " + err.Error())
		return ""
	}
	return string(encoded)
}

func unmarshalUpstreamGroupModels(encoded string) []string {
	if strings.TrimSpace(encoded) == "" {
		return nil
	}
	var models []string
	if err := common.Unmarshal([]byte(encoded), &models); err != nil {
		common.SysError("failed to unmarshal upstream group models: " + err.Error())
		return nil
	}
	return models
}

func managedUpstreamChannelCount(providerID int, remoteGroupID string) (int64, error) {
	var count int64
	err := model.DB.Model(&model.Channel{}).
		Where("upstream_provider_id = ? AND upstream_group_id = ?", providerID, remoteGroupID).
		Count(&count).Error
	return count, err
}

func managedUpstreamProviderStatus(status int) string {
	if status == upstreamProviderStatusActive {
		return "active"
	}
	return "disabled"
}

func newUpstreamProvisionKeyName(providerID int, prefix string) (string, error) {
	random, err := common.GenerateRandomCharsKey(12)
	if err != nil {
		return "", err
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "new-api-upstream"
	}
	prefix = strings.Join(strings.Fields(prefix), "-")
	if len(prefix) > 24 {
		prefix = prefix[:24]
	}
	return fmt.Sprintf("%s-%d-%s", prefix, providerID, random), nil
}

func createManagedUpstreamChannel(
	provider *model.UpstreamProvider,
	group *model.UpstreamGroup,
	remoteKey *upstreamProviderRemoteKey,
	models []string,
	localGroup string,
	prefix string,
) (*model.Channel, error) {
	if provider == nil || group == nil || remoteKey == nil || remoteKey.Key == "" {
		return nil, errors.New("upstream key data is invalid")
	}
	if remoteKey.GroupID != "" && remoteKey.GroupID != group.RemoteGroupID {
		return nil, errors.New("upstream API key was assigned to an unexpected group")
	}
	models = normalizeUpstreamModelIDs(models)
	if len(models) == 0 {
		return nil, errors.New("upstream returned no usable models")
	}
	if remoteKey.ID == "" {
		return nil, errors.New("upstream returned no API key ID")
	}
	var existing int64
	if err := model.DB.Model(&model.Channel{}).
		Where("upstream_provider_id = ? AND upstream_key_id = ?", provider.Id, remoteKey.ID).
		Count(&existing).Error; err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, errors.New("a local channel is already bound to this upstream API key")
	}
	rate := group.RateMultiplier
	if group.EffectiveRateMultiplier != nil {
		rate = *group.EffectiveRateMultiplier
	}
	if !upstreamValidMultiplier(rate) {
		return nil, errors.New("upstream group rate is invalid")
	}
	costRate := rate * provider.RateCorrection
	if !upstreamValidMultiplier(costRate) {
		return nil, errors.New("upstream group cost rate is invalid")
	}
	channelType := constant.ChannelTypeSub2API
	if provider.Type == model.UpstreamProviderTypeCodeGo || provider.Type == model.UpstreamProviderTypeNewAPI {
		channelType = constant.ChannelTypeNewAPI
	}
	baseURL := provider.BaseURL
	providerID := provider.Id
	remoteUserID := provider.UpstreamUserID
	remoteGroupID := group.RemoteGroupID
	remoteKeyID := remoteKey.ID
	name := buildManagedUpstreamChannelName(prefix, provider.Name, group.Name)
	channel := &model.Channel{
		Type:               channelType,
		Key:                remoteKey.Key,
		Status:             common.ChannelStatusManuallyDisabled,
		Name:               name,
		CreatedTime:        common.GetTimestamp(),
		BaseURL:            &baseURL,
		BalanceUpdatedTime: common.GetTimestamp(),
		Models:             strings.Join(models, ","),
		Group:              localGroup,
		UpstreamProviderID: &providerID,
		UpstreamGroupID:    &remoteGroupID,
		UpstreamKeyID:      &remoteKeyID,
		UpstreamRate:       &rate,
		UpstreamCostRate:   &costRate,
	}
	if remoteUserID != "" {
		channel.UpstreamRemoteID = &remoteUserID
	}
	if provider.Balance != nil {
		channel.Balance = *provider.Balance
	}
	tx := model.DB.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	lockedProvider, err := model.GetUpstreamProviderByIdForUpdate(tx, provider.Id)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if lockedProvider.Status != upstreamProviderStatusActive {
		tx.Rollback()
		return nil, errors.New("upstream provider is disabled")
	}
	if lockedProvider.Type != provider.Type || lockedProvider.BaseURL != provider.BaseURL {
		tx.Rollback()
		return nil, errors.New("upstream provider changed while the API key was being created; synchronize and try again")
	}
	lockedGroup, err := model.GetUpstreamGroupByRemoteIDForUpdate(tx, provider.Id, group.RemoteGroupID)
	if err != nil {
		tx.Rollback()
		return nil, errors.New("upstream group changed while the API key was being created; synchronize and try again")
	}
	if lockedGroup.IsDynamic {
		tx.Rollback()
		return nil, errors.New("upstream group is dynamic and cannot be bound to a static local channel")
	}
	provider = lockedProvider
	group = lockedGroup
	rate = group.RateMultiplier
	if group.EffectiveRateMultiplier != nil {
		rate = *group.EffectiveRateMultiplier
	}
	if !upstreamValidMultiplier(rate) {
		tx.Rollback()
		return nil, errors.New("upstream group rate is invalid")
	}
	costRate = rate * provider.RateCorrection
	if !upstreamValidMultiplier(costRate) {
		tx.Rollback()
		return nil, errors.New("upstream group cost rate is invalid")
	}
	baseURL = provider.BaseURL
	remoteUserID = provider.UpstreamUserID
	remoteGroupID = group.RemoteGroupID
	channel.Name = buildManagedUpstreamChannelName(prefix, provider.Name, group.Name)
	if remoteUserID == "" {
		channel.UpstreamRemoteID = nil
	} else {
		channel.UpstreamRemoteID = &remoteUserID
	}
	channel.Balance = 0
	if provider.Balance != nil {
		channel.Balance = *provider.Balance
	}
	if err := tx.Create(channel).Error; err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := channel.AddAbilities(tx); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return channel, nil
}

func buildManagedUpstreamChannelName(prefix, providerName, groupName string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = providerName
	}
	name := strings.Trim(strings.Join([]string{prefix, groupName}, "-"), "-")
	if name == "" {
		name = "upstream-channel"
	}
	runes := []rune(name)
	if len(runes) > 120 {
		return string(runes[:120])
	}
	return name
}
