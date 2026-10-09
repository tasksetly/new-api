package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type upstreamProviderClientTestRequest struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

func findUpstreamProviderClientTestRequest(t *testing.T, requests []upstreamProviderClientTestRequest, method, path string) upstreamProviderClientTestRequest {
	t.Helper()
	for _, request := range requests {
		if request.Method == method && request.Path == path {
			return request
		}
	}
	t.Fatalf("did not receive %s %s", method, path)
	return upstreamProviderClientTestRequest{}
}

func disableUpstreamProviderClientTestSSRFProtection(t *testing.T) {
	t.Helper()
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	t.Cleanup(func() {
		*fetchSetting = originalFetchSetting
	})
	fetchSetting.EnableSSRFProtection = false
}

func TestNewAPIUpstreamClientUsesManualManagementToken(t *testing.T) {
	disableUpstreamProviderClientTestSSRFProtection(t)

	var requestMutex sync.Mutex
	requests := make([]upstreamProviderClientTestRequest, 0, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, "cannot read request", http.StatusBadRequest)
			return
		}
		requestMutex.Lock()
		requests = append(requests, upstreamProviderClientTestRequest{
			Method: request.Method,
			Path:   request.URL.Path,
			Query:  request.URL.Query(),
			Header: request.Header.Clone(),
			Body:   body,
		})
		requestMutex.Unlock()

		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /api/user/self":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"id":42,"username":"newapi-user","quota":2500000}}`))
		case http.MethodGet + " /api/status":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		case http.MethodGet + " /api/user/self/groups":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"vip":{"ratio":0.5,"desc":"VIP group"},"auto":{"ratio":"自动","desc":"Automatic group"}}}`))
		case http.MethodGet + " /api/log/self/stat":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"quota":1000000}}`))
		case http.MethodPost + " /api/token/":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"id":7}}`))
		case http.MethodGet + " /api/token/search":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"items":[{"id":7,"name":"local-channel"}]}}`))
		case http.MethodPost + " /api/token/7/key":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"key":"sk-newapi-created"}}`))
		case http.MethodDelete + " /api/token/7":
			_, _ = writer.Write([]byte(`{"success":true,"data":true}`))
		case http.MethodGet + " /v1/models":
			_, _ = writer.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o"},{"id":""}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client, err := newNewAPIUpstreamClient(server.URL)
	require.NoError(t, err)

	ctx := context.Background()
	session := &upstreamProviderRemoteSession{Token: "  Bearer management-token  "}

	profile, err := client.Profile(ctx, *session)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, "42", profile.RemoteUserID)
	assert.Equal(t, "newapi-user", profile.Username)
	require.NotNil(t, profile.Balance)
	assert.Equal(t, 5.0, *profile.Balance)

	groups, err := client.Groups(ctx, *session)
	require.NoError(t, err)
	require.Len(t, groups, 2)
	groupsByID := make(map[string]upstreamProviderRemoteGroup, len(groups))
	for _, group := range groups {
		groupsByID[group.RemoteID] = group
	}
	require.NotNil(t, groupsByID["vip"].EffectiveRate)
	assert.Equal(t, 0.5, groupsByID["vip"].Rate)
	assert.Equal(t, 0.5, *groupsByID["vip"].EffectiveRate)
	assert.False(t, groupsByID["vip"].IsDynamic)
	assert.True(t, groupsByID["auto"].IsDynamic)
	assert.Equal(t, "Automatic group", groupsByID["auto"].Description)

	usage, err := client.Usage(ctx, *session)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.NotNil(t, usage.Cost30Days)
	assert.Equal(t, 2.0, *usage.Cost30Days)

	remoteKey, err := client.CreateKey(ctx, *session, "local-channel", "vip")
	require.NoError(t, err)
	require.NotNil(t, remoteKey)
	assert.Equal(t, "7", remoteKey.ID)
	assert.Equal(t, "sk-newapi-created", remoteKey.Key)
	assert.Equal(t, "vip", remoteKey.GroupID)

	models, err := client.Models(ctx, remoteKey.Key)
	require.NoError(t, err)
	assert.Equal(t, []string{"gpt-4o"}, models)
	require.NoError(t, client.DeleteKey(ctx, *session, remoteKey.ID))

	requestMutex.Lock()
	recordedRequests := append([]upstreamProviderClientTestRequest(nil), requests...)
	requestMutex.Unlock()
	require.Len(t, recordedRequests, 12)

	for _, path := range []string{"/api/user/self", "/api/user/self/groups", "/api/log/self/stat", "/api/status", "/api/token/", "/api/token/search", "/api/token/7/key", "/api/token/7"} {
		method := http.MethodGet
		if path == "/api/token/" || path == "/api/token/7/key" {
			method = http.MethodPost
		} else if path == "/api/token/7" {
			method = http.MethodDelete
		}
		recordedRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, method, path)
		assert.Equal(t, "Bearer management-token", recordedRequest.Header.Get("Authorization"))
		assert.Empty(t, recordedRequest.Header.Get("CodeGo-Api-User"))
	}

	createRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, http.MethodPost, "/api/token/")
	var createPayload map[string]any
	require.NoError(t, common.Unmarshal(createRequest.Body, &createPayload))
	assert.Equal(t, "local-channel", createPayload["name"])
	assert.Equal(t, "vip", createPayload["group"])
	assert.Equal(t, true, createPayload["unlimited_quota"])
	assert.Equal(t, false, createPayload["model_limits_enabled"])

	searchRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, http.MethodGet, "/api/token/search")
	assert.Equal(t, "local-channel", searchRequest.Query.Get("keyword"))
	assert.Equal(t, "1", searchRequest.Query.Get("p"))
	assert.Equal(t, "100", searchRequest.Query.Get("size"))

	modelRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, http.MethodGet, "/v1/models")
	assert.Equal(t, "Bearer sk-newapi-created", modelRequest.Header.Get("Authorization"))
	usageRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, http.MethodGet, "/api/log/self/stat")
	assert.NotEmpty(t, usageRequest.Query.Get("start_timestamp"))
	assert.NotEmpty(t, usageRequest.Query.Get("end_timestamp"))
}

func TestNormalizeUpstreamBearerToken(t *testing.T) {
	assert.Equal(t, "management-token", normalizeUpstreamBearerToken("management-token"))
	assert.Equal(t, "management-token", normalizeUpstreamBearerToken("  bearer management-token  "))
	assert.Empty(t, normalizeUpstreamBearerToken("Bearer management-token extra"))
	assert.Empty(t, normalizeUpstreamBearerToken("  "))
}

func TestNewAPIUpstreamClientCleansUpTokenWhenDiscoveryFails(t *testing.T) {
	disableUpstreamProviderClientTestSSRFProtection(t)
	var deleteCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case http.MethodPost + " /api/token/":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"id":8}}`))
		case http.MethodGet + " /api/token/search":
			_, _ = writer.Write([]byte(`{"success":false,"message":"temporary failure"}`))
		case http.MethodDelete + " /api/token/8":
			deleteCount.Add(1)
			_, _ = writer.Write([]byte(`{"success":true,"data":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client, err := newNewAPIUpstreamClient(server.URL)
	require.NoError(t, err)
	_, err = client.CreateKey(context.Background(), upstreamProviderRemoteSession{Token: "management-token"}, "provisioned", "vip")
	require.Error(t, err)
	assert.Equal(t, int32(1), deleteCount.Load())
}

func TestNewAPIUpstreamClientDoesNotLoginOrRefresh(t *testing.T) {
	disableUpstreamProviderClientTestSSRFProtection(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
	}))
	t.Cleanup(server.Close)

	client, err := newNewAPIUpstreamClient(server.URL)
	require.NoError(t, err)
	_, err = client.Login(context.Background(), upstreamProviderCredentials{Username: "user", Password: "password"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manually supplied")
	_, err = client.Refresh(context.Background(), upstreamProviderCredentials{Token: "expired"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "replaced manually")
}

func TestCodeGoManagementHeadersRequireRemoteUserID(t *testing.T) {
	client := &codeGoUpstreamClient{}
	headers, err := client.managementHeaders(upstreamProviderRemoteSession{
		Token:        "Bearer management-token",
		RemoteUserID: "42",
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer management-token", headers.Get("Authorization"))
	assert.Equal(t, "42", headers.Get("CodeGo-Api-User"))
	assert.Equal(t, "42", headers.Get("New-Api-User"))

	_, err = client.managementHeaders(upstreamProviderRemoteSession{Token: "management-token"})
	assert.Error(t, err)
}

func TestCodeGoGroupsIncludeModelsPlatformsAndSuccessRate(t *testing.T) {
	disableUpstreamProviderClientTestSSRFProtection(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/marketplace/key-group-options":
			assert.Equal(t, "Bearer management-token", request.Header.Get("Authorization"))
			assert.Equal(t, "42", request.Header.Get("New-Api-User"))
			_, _ = writer.Write([]byte(`{"success":true,"data":[{"value":"vip","label":"VIP","description":"VIP","ratio":0.5}]}`))
		case "/api/user/self/group-status":
			_, _ = writer.Write([]byte(`{"success":true,"data":[{"group":"vip","request_count":10,"models":[{"model":"gpt-4o","success_rate":90,"request_count":10}]}]}`))
		case "/api/pricing":
			_, _ = writer.Write([]byte(`{"success":true,"data":[{"model_name":"gpt-4o","owner_by":"openai","enable_groups":["vip"]}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client, err := newCodeGoUpstreamClient(server.URL)
	require.NoError(t, err)
	groups, err := client.Groups(context.Background(), upstreamProviderRemoteSession{Token: "management-token", RemoteUserID: "42"})
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "VIP", groups[0].Name)
	assert.Equal(t, "VIP", groups[0].Description)
	assert.Equal(t, "openai", groups[0].Platform)
	assert.Equal(t, []string{"gpt-4o"}, groups[0].Models)
	assert.Equal(t, int64(10), groups[0].RequestCount)
	require.NotNil(t, groups[0].SuccessRate)
	assert.Equal(t, 90.0, *groups[0].SuccessRate)
}

func TestSub2APIUpstreamClientUsesUserManagementProtocol(t *testing.T) {
	disableUpstreamProviderClientTestSSRFProtection(t)

	var requestMutex sync.Mutex
	requests := make([]upstreamProviderClientTestRequest, 0, 6)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, "cannot read request", http.StatusBadRequest)
			return
		}
		requestMutex.Lock()
		requests = append(requests, upstreamProviderClientTestRequest{
			Method: request.Method,
			Path:   request.URL.Path,
			Query:  request.URL.Query(),
			Header: request.Header.Clone(),
			Body:   body,
		})
		requestMutex.Unlock()

		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case http.MethodPost + " /api/v1/auth/login":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"access_token":"sub2-access","refresh_token":"sub2-refresh","expires_in":3600}}`))
		case http.MethodGet + " /api/v1/user/profile":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"id":11,"email":"operator@example.com","balance":12.5,"frozen_balance":1.5,"concurrency":3}}`))
		case http.MethodGet + " /api/v1/groups/available":
			_, _ = writer.Write([]byte(`{"code":0,"data":[{"id":8,"name":"Pro","platform":"openai","subscription_type":"monthly","rate_multiplier":1.2,"peak_rate_enabled":true,"peak_rate_multiplier":1.8,"peak_start":"18:00","peak_end":"22:00"}]}`))
		case http.MethodGet + " /api/v1/groups/rates":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"8":1.5}}`))
		case http.MethodGet + " /api/v1/usage/stats":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"total_actual_cost":0.75}}`))
		case http.MethodPost + " /api/v1/keys":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"id":9,"key":"sk-sub2-created","name":"local-channel","group_id":8}}`))
		case http.MethodDelete + " /api/v1/keys/9":
			_, _ = writer.Write([]byte(`{"code":0,"data":true}`))
		case http.MethodGet + " /v1/models":
			_, _ = writer.Write([]byte(`{"data":[{"id":"gpt-4.1"},{"id":"gpt-4.1-mini"}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client, err := newSub2APIUpstreamClient(server.URL)
	require.NoError(t, err)

	ctx := context.Background()
	session, err := client.Login(ctx, upstreamProviderCredentials{Username: "operator@example.com", Password: "password"})
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, "sub2-access", session.Token)
	assert.Equal(t, "sub2-refresh", session.RefreshToken)
	require.NotNil(t, session.ExpiresAt)

	profile, err := client.Profile(ctx, *session)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, "11", profile.RemoteUserID)
	assert.Equal(t, "operator@example.com", profile.Username)
	require.NotNil(t, profile.Balance)
	assert.Equal(t, 12.5, *profile.Balance)
	require.NotNil(t, profile.Frozen)
	assert.Equal(t, 1.5, *profile.Frozen)
	require.NotNil(t, profile.Concurrency)
	assert.Equal(t, 3, *profile.Concurrency)

	groups, err := client.Groups(ctx, *session)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "8", groups[0].RemoteID)
	assert.Equal(t, "Pro", groups[0].Name)
	assert.Equal(t, 1.2, groups[0].Rate)
	require.NotNil(t, groups[0].EffectiveRate)
	assert.Equal(t, 1.5, *groups[0].EffectiveRate)
	assert.True(t, groups[0].PeakRateEnabled)
	require.NotNil(t, groups[0].PeakRateMultiplier)
	assert.Equal(t, 1.8, *groups[0].PeakRateMultiplier)

	usage, err := client.Usage(ctx, *session)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.NotNil(t, usage.Cost30Days)
	assert.Equal(t, 0.75, *usage.Cost30Days)

	remoteKey, err := client.CreateKey(ctx, *session, "local-channel", "8")
	require.NoError(t, err)
	require.NotNil(t, remoteKey)
	assert.Equal(t, "9", remoteKey.ID)
	assert.Equal(t, "sk-sub2-created", remoteKey.Key)
	assert.Equal(t, "8", remoteKey.GroupID)

	models, err := client.Models(ctx, remoteKey.Key)
	require.NoError(t, err)
	assert.Equal(t, []string{"gpt-4.1", "gpt-4.1-mini"}, models)
	require.NoError(t, client.DeleteKey(ctx, *session, remoteKey.ID))

	requestMutex.Lock()
	recordedRequests := append([]upstreamProviderClientTestRequest(nil), requests...)
	requestMutex.Unlock()
	require.Len(t, recordedRequests, 8)

	loginRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, http.MethodPost, "/api/v1/auth/login")
	var loginPayload map[string]string
	require.NoError(t, common.Unmarshal(loginRequest.Body, &loginPayload))
	assert.Equal(t, map[string]string{"email": "operator@example.com", "password": "password"}, loginPayload)

	for _, path := range []string{"/api/v1/user/profile", "/api/v1/groups/available", "/api/v1/groups/rates", "/api/v1/usage/stats", "/api/v1/keys", "/api/v1/keys/9"} {
		method := http.MethodGet
		if path == "/api/v1/keys" {
			method = http.MethodPost
		} else if path == "/api/v1/keys/9" {
			method = http.MethodDelete
		}
		recordedRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, method, path)
		assert.Equal(t, "Bearer sub2-access", recordedRequest.Header.Get("Authorization"))
	}

	createRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, http.MethodPost, "/api/v1/keys")
	var createPayload map[string]any
	require.NoError(t, common.Unmarshal(createRequest.Body, &createPayload))
	assert.Equal(t, "local-channel", createPayload["name"])
	assert.Equal(t, float64(8), createPayload["group_id"])

	modelRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, http.MethodGet, "/v1/models")
	assert.Equal(t, "Bearer sk-sub2-created", modelRequest.Header.Get("Authorization"))
	usageRequest := findUpstreamProviderClientTestRequest(t, recordedRequests, http.MethodGet, "/api/v1/usage/stats")
	assert.Equal(t, "month", usageRequest.Query.Get("period"))
}

func TestUpdateManagedUpstreamProviderRefreshesBoundChannelCostsAndGuardsProtocolChanges(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "upstream-provider-test-secret")
	require.NoError(t, model.DB.AutoMigrate(&model.UpstreamProvider{}, &model.UpstreamGroup{}))
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM upstream_groups")
		model.DB.Exec("DELETE FROM upstream_providers")
	})

	token, err := common.EncryptUpstreamCredential("management-token")
	require.NoError(t, err)
	provider := &model.UpstreamProvider{
		Name:           "upstream-provider-service-test",
		Type:           model.UpstreamProviderTypeNewAPI,
		BaseURL:        "https://upstream.example",
		TokenEncrypted: token,
		UpstreamUserID: "42",
		RateCorrection: 1,
	}
	require.NoError(t, model.CreateUpstreamProvider(provider))

	providerID := provider.Id
	groupID := "vip"
	rate := 1.5
	costRate := 1.5
	channel := &model.Channel{
		Name:               "bound-upstream-channel",
		Key:                "sk-test",
		UpstreamProviderID: &providerID,
		UpstreamGroupID:    &groupID,
		UpstreamRate:       &rate,
		UpstreamCostRate:   &costRate,
	}
	require.NoError(t, model.DB.Create(channel).Error)

	rateCorrection := 1.2
	updated, err := UpdateManagedUpstreamProvider(provider.Id, UpstreamProviderMutation{
		RateCorrection: &rateCorrection,
	})
	require.NoError(t, err)
	assert.Equal(t, rateCorrection, updated.RateCorrection)

	var storedChannel model.Channel
	require.NoError(t, model.DB.First(&storedChannel, channel.Id).Error)
	require.NotNil(t, storedChannel.UpstreamCostRate)
	assert.InDelta(t, 1.8, *storedChannel.UpstreamCostRate, 0.000001)

	providerType := model.UpstreamProviderTypeSub2API
	_, err = UpdateManagedUpstreamProvider(provider.Id, UpstreamProviderMutation{Type: &providerType})
	require.Error(t, err)
	assert.ErrorContains(t, err, "while local channels are bound")

	var storedProvider model.UpstreamProvider
	require.NoError(t, model.DB.First(&storedProvider, provider.Id).Error)
	assert.Equal(t, model.UpstreamProviderTypeNewAPI, storedProvider.Type)

	staleProvider := storedProvider
	newToken := "new-management-token"
	_, err = UpdateManagedUpstreamProvider(provider.Id, UpstreamProviderMutation{Token: &newToken})
	require.NoError(t, err)
	require.ErrorIs(t, persistManagedUpstreamSession(&staleProvider, &upstreamProviderRemoteSession{
		Token: "stale-management-token",
	}), errUpstreamProviderConfigurationChanged)

	require.NoError(t, model.DB.First(&storedProvider, provider.Id).Error)
	storedToken, err := common.DecryptUpstreamCredential(storedProvider.TokenEncrypted)
	require.NoError(t, err)
	assert.Equal(t, newToken, storedToken)
	assert.Greater(t, storedProvider.ConfigVersion, staleProvider.ConfigVersion)
}

func TestSyncManagedUpstreamProviderPersistsRemoteUsageCost(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "upstream-provider-cost-sync-test-secret")
	disableUpstreamProviderClientTestSSRFProtection(t)
	require.NoError(t, model.DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM channels").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM upstream_groups").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM upstream_providers").Error)
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM abilities")
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM upstream_groups")
		model.DB.Exec("DELETE FROM upstream_providers")
	})

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /api/v1/user/profile":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"id":11,"email":"operator@example.com","balance":12.5,"frozen_balance":1.5,"concurrency":3}}`))
		case http.MethodGet + " /api/v1/groups/available":
			_, _ = writer.Write([]byte(`{"code":0,"data":[{"id":8,"name":"Pro","rate_multiplier":1.2}]}`))
		case http.MethodGet + " /api/v1/groups/rates":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"8":1.5}}`))
		case http.MethodGet + " /api/v1/usage/stats":
			if request.URL.Query().Get("period") != "month" {
				http.Error(writer, "expected rolling month usage", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`{"code":0,"data":{"total_actual_cost":0.75}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	token, err := common.EncryptUpstreamCredential("sub2-access")
	require.NoError(t, err)
	provider := &model.UpstreamProvider{
		Name:           "upstream-cost-sync",
		Type:           model.UpstreamProviderTypeSub2API,
		BaseURL:        server.URL,
		TokenEncrypted: token,
		RateCorrection: 1,
		Status:         upstreamProviderStatusActive,
		SyncEnabled:    true,
	}
	require.NoError(t, model.CreateUpstreamProvider(provider))

	view, err := SyncManagedUpstreamProvider(context.Background(), provider.Id)
	require.NoError(t, err)
	require.NotNil(t, view.Balance)
	assert.Equal(t, 12.5, *view.Balance)
	require.NotNil(t, view.UpstreamCost30Days)
	assert.Equal(t, 0.75, *view.UpstreamCost30Days)
	require.NotNil(t, view.UpstreamCostAt)
	assert.Empty(t, view.LastSyncError)

	var stored model.UpstreamProvider
	require.NoError(t, model.DB.First(&stored, provider.Id).Error)
	require.NotNil(t, stored.UpstreamCost30Days)
	assert.Equal(t, 0.75, *stored.UpstreamCost30Days)
	require.NotNil(t, stored.UpstreamCostAt)
	groups, err := model.ListUpstreamGroups(provider.Id)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.NotNil(t, groups[0].EffectiveRateMultiplier)
	assert.Equal(t, 1.5, *groups[0].EffectiveRateMultiplier)
}

func TestProvisionManagedUpstreamChannelsRemovesUnboundRemoteKey(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "upstream-provider-provision-cleanup-test-secret")
	disableUpstreamProviderClientTestSSRFProtection(t)
	require.NoError(t, model.DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM channels").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM upstream_groups").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM upstream_providers").Error)
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM abilities")
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM upstream_groups")
		model.DB.Exec("DELETE FROM upstream_providers")
	})

	var deleted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case http.MethodPost + " /api/v1/keys":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"id":9,"key":"sk-sub2-created","name":"local-channel","group_id":8}}`))
		case http.MethodGet + " /v1/models":
			http.Error(writer, "remote model lookup failed", http.StatusInternalServerError)
		case http.MethodDelete + " /api/v1/keys/9":
			deleted.Add(1)
			_, _ = writer.Write([]byte(`{"code":0,"data":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	token, err := common.EncryptUpstreamCredential("sub2-access")
	require.NoError(t, err)
	provider := &model.UpstreamProvider{
		Name:           "upstream-provision-cleanup",
		Type:           model.UpstreamProviderTypeSub2API,
		BaseURL:        server.URL,
		TokenEncrypted: token,
		RateCorrection: 1,
		Status:         upstreamProviderStatusActive,
	}
	require.NoError(t, model.CreateUpstreamProvider(provider))
	require.NoError(t, model.DB.Create(&model.UpstreamGroup{
		UpstreamProviderID: provider.Id,
		RemoteGroupID:      "8",
		Name:               "Pro",
		RateMultiplier:     1,
	}).Error)

	results, err := ProvisionManagedUpstreamChannels(context.Background(), UpstreamProvisionInput{
		ProviderID:     provider.Id,
		RemoteGroupIDs: []string{"8"},
		LocalGroup:     "default",
		NamePrefix:     "local-channel",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Zero(t, results[0].ChannelID)
	assert.Equal(t, "could not bind the created upstream API key; cleanup was requested", results[0].Error)
	assert.Equal(t, int32(1), deleted.Load())
	var channelCount int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("upstream_provider_id = ?", provider.Id).Count(&channelCount).Error)
	assert.Zero(t, channelCount)
}

func TestUpstreamProviderClientBlocksCrossOriginRedirects(t *testing.T) {
	disableUpstreamProviderClientTestSSRFProtection(t)
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		destinationRequests.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client, err := newSub2APIUpstreamClient(source.URL)
	require.NoError(t, err)
	_, _, err = client.request(context.Background(), http.MethodPost, "/api/v1/auth/login", nil, map[string]string{
		"email": "operator@example.com", "password": "redirect-secret",
	}, nil)
	require.Error(t, err)
	assert.Zero(t, destinationRequests.Load())
}
func TestNewAPIManualTokenErrorsAreActionable(t *testing.T) {
	assert.Equal(t, "upstream management access token expired or was rejected; replace it manually", PublicManagedUpstreamError(errUpstreamProviderManualToken))
	assert.Equal(t, "upstream management access token is missing", PublicManagedUpstreamError(errors.New("NewAPI management access token is missing")))
}

func TestUpstreamProviderResponseMessagesClassifyNewAPIAuthorizationFailures(t *testing.T) {
	for _, message := range []string{"无权访问", "权限不足", "access denied", "forbidden"} {
		t.Run(message, func(t *testing.T) {
			err := upstreamProviderResponseError(message)
			require.ErrorIs(t, err, errUpstreamProviderUnauthorized)
		})
	}
}

func TestUpstreamProviderResponseMessagesDoNotExposeRemoteBody(t *testing.T) {
	secret := "remote-response-secret"
	err := upstreamProviderResponseError("the upstream reflected " + secret)
	require.ErrorIs(t, err, errUpstreamProviderRejected)
	message := PublicManagedUpstreamError(err)
	assert.Equal(t, "upstream rejected the request", message)
	assert.NotContains(t, message, secret)
}
