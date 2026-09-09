package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// managedUpstreamProviderMutationRequest intentionally keeps credentials only
// in the request type. Management responses use service.UpstreamProviderView,
// which has no reusable credential values.
type managedUpstreamProviderMutationRequest struct {
	Name           *string  `json:"name"`
	Type           *string  `json:"type"`
	BaseURL        *string  `json:"base_url"`
	Username       *string  `json:"username"`
	Password       *string  `json:"password"`
	Token          *string  `json:"token"`
	RefreshToken   *string  `json:"refresh_token"`
	TOTPSecret     *string  `json:"totp_secret"`
	UpstreamUserID *string  `json:"upstream_user_id"`
	RateCorrection *float64 `json:"rate_correction"`
	SyncEnabled    *bool    `json:"sync_enabled"`
}

func (request managedUpstreamProviderMutationRequest) mutation() service.UpstreamProviderMutation {
	return service.UpstreamProviderMutation{
		Name:           request.Name,
		Type:           request.Type,
		BaseURL:        request.BaseURL,
		Username:       request.Username,
		Password:       request.Password,
		Token:          request.Token,
		RefreshToken:   request.RefreshToken,
		TOTPSecret:     request.TOTPSecret,
		UpstreamUserID: request.UpstreamUserID,
		RateCorrection: request.RateCorrection,
		SyncEnabled:    request.SyncEnabled,
	}
}

type managedUpstreamProvisionRequest struct {
	RemoteGroupIDs []string `json:"remote_group_ids"`
	LocalGroup     string   `json:"local_group"`
	NamePrefix     string   `json:"name_prefix"`
}

func managedUpstreamProviderID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid upstream provider ID")
		return 0, false
	}
	return id, true
}

func ListManagedUpstreamProviders(c *gin.Context) {
	page, err := service.ListManagedUpstreamProviders(common.GetPageQuery(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, page)
}

func GetManagedUpstreamProvider(c *gin.Context) {
	id, ok := managedUpstreamProviderID(c)
	if !ok {
		return
	}
	provider, err := service.GetManagedUpstreamProvider(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, provider)
}

func CreateManagedUpstreamProvider(c *gin.Context) {
	var request managedUpstreamProviderMutationRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid upstream provider request"})
		return
	}
	provider, err := service.CreateManagedUpstreamProvider(request.mutation())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_provider.create", map[string]any{
		"id":           provider.ID,
		"name":         provider.Name,
		"type":         provider.Type,
		"base_url":     provider.BaseURL,
		"sync_enabled": provider.SyncEnabled,
	})
	common.ApiSuccess(c, provider)
}

func UpdateManagedUpstreamProvider(c *gin.Context) {
	id, ok := managedUpstreamProviderID(c)
	if !ok {
		return
	}
	var request managedUpstreamProviderMutationRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid upstream provider request"})
		return
	}
	provider, err := service.UpdateManagedUpstreamProvider(id, request.mutation())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_provider.update", map[string]any{
		"id":           provider.ID,
		"name":         provider.Name,
		"type":         provider.Type,
		"base_url":     provider.BaseURL,
		"sync_enabled": provider.SyncEnabled,
	})
	common.ApiSuccess(c, provider)
}

func DeleteManagedUpstreamProvider(c *gin.Context) {
	id, ok := managedUpstreamProviderID(c)
	if !ok {
		return
	}
	provider, err := service.GetManagedUpstreamProvider(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.DeleteManagedUpstreamProvider(id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_provider.delete", map[string]any{
		"id":   id,
		"name": provider.Name,
		"type": provider.Type,
	})
	common.ApiSuccess(c, nil)
}

func TestManagedUpstreamProvider(c *gin.Context) {
	id, ok := managedUpstreamProviderID(c)
	if !ok {
		return
	}
	profile, err := service.TestManagedUpstreamProvider(c.Request.Context(), id)
	if err != nil {
		common.ApiErrorMsg(c, service.PublicManagedUpstreamError(err))
		return
	}
	recordManageAudit(c, "upstream_provider.test", map[string]any{"id": id})
	common.ApiSuccess(c, gin.H{
		"upstream_user_id":     profile.RemoteUserID,
		"username":             profile.Username,
		"balance":              profile.Balance,
		"frozen_balance":       profile.Frozen,
		"upstream_concurrency": profile.Concurrency,
	})
}

func SyncManagedUpstreamProvider(c *gin.Context) {
	id, ok := managedUpstreamProviderID(c)
	if !ok {
		return
	}
	provider, err := service.SyncManagedUpstreamProvider(c.Request.Context(), id)
	if err != nil {
		common.ApiErrorMsg(c, service.PublicManagedUpstreamError(err))
		return
	}
	recordManageAudit(c, "upstream_provider.sync", map[string]any{
		"id":   provider.ID,
		"name": provider.Name,
	})
	common.ApiSuccess(c, provider)
}

func SyncAllManagedUpstreamProviders(c *gin.Context) {
	results, err := service.SyncAllManagedUpstreamProviders(c.Request.Context())
	if err != nil {
		common.ApiErrorMsg(c, service.PublicManagedUpstreamError(err))
		return
	}
	failed := 0
	for _, result := range results {
		if result.Error != "" {
			failed++
		}
	}
	recordManageAudit(c, "upstream_provider.sync_all", map[string]any{
		"provider_count": len(results),
		"failed_count":   failed,
	})
	common.ApiSuccess(c, gin.H{"items": results})
}

func GetManagedUpstreamProviderGroups(c *gin.Context) {
	id, ok := managedUpstreamProviderID(c)
	if !ok {
		return
	}
	groups, err := service.ListManagedUpstreamGroups(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": groups})
}

// GetManagedUpstreamGroupComparison returns each provider with its current
// group snapshot, so the UI can compare synchronized rates across providers.
func GetManagedUpstreamGroupComparison(c *gin.Context) {
	providers := make([]*service.UpstreamProviderView, 0)
	for pageNumber := 1; ; pageNumber++ {
		page, err := service.ListManagedUpstreamProviders(&common.PageInfo{Page: pageNumber, PageSize: 100})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		providers = append(providers, page.Items...)
		if page.PageInfo == nil || len(providers) >= page.PageInfo.Total || len(page.Items) == 0 {
			break
		}
	}

	type comparisonItem struct {
		Provider *service.UpstreamProviderView        `json:"provider"`
		Groups   []*service.UpstreamProviderGroupView `json:"groups"`
	}
	items := make([]comparisonItem, 0, len(providers))
	for _, provider := range providers {
		groups, err := service.ListManagedUpstreamGroups(provider.ID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		items = append(items, comparisonItem{Provider: provider, Groups: groups})
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func ProvisionManagedUpstreamChannels(c *gin.Context) {
	id, ok := managedUpstreamProviderID(c)
	if !ok {
		return
	}
	var request managedUpstreamProvisionRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid upstream channel provisioning request"})
		return
	}
	results, err := service.ProvisionManagedUpstreamChannels(c.Request.Context(), service.UpstreamProvisionInput{
		ProviderID:     id,
		RemoteGroupIDs: request.RemoteGroupIDs,
		LocalGroup:     request.LocalGroup,
		NamePrefix:     request.NamePrefix,
	})
	if err != nil {
		common.ApiErrorMsg(c, service.PublicManagedUpstreamError(err))
		return
	}
	failed := 0
	for _, result := range results {
		if result.Error != "" {
			failed++
		}
	}
	recordManageAudit(c, "upstream_provider.provision", map[string]any{
		"provider_id":        id,
		"remote_group_count": len(request.RemoteGroupIDs),
		"local_group":        request.LocalGroup,
		"result_count":       len(results),
		"failed_count":       failed,
	})
	common.ApiSuccess(c, gin.H{"items": results})
}
