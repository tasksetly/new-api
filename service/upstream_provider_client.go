package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/pquerna/otp/totp"
)

const (
	upstreamProviderRequestTimeout = 15 * time.Second
	upstreamProviderMaxBodyBytes   = 512 * 1024
	upstreamProviderTokenSkew      = 5 * time.Minute
)

var (
	errUpstreamProviderUnauthorized = errors.New("upstream rejected the credentials")
	errUpstreamProviderTOTPRequired = errors.New("upstream requires two-factor authentication; save its TOTP secret first")
	errUpstreamProviderRejected     = errors.New("upstream rejected the request")
)

// upstreamProviderCredentials only exists while an upstream request is being
// made. Its fields must never be serialized, logged, or returned to callers.
type upstreamProviderCredentials struct {
	Username   string
	Password   string
	Token      string
	Refresh    string
	TOTPSecret string
	RemoteUser string
	ExpiresAt  *time.Time
}

type upstreamProviderRemoteSession struct {
	Token        string
	RefreshToken string
	RemoteUserID string
	ExpiresAt    *time.Time
	Cookie       string
}

type upstreamProviderRemoteProfile struct {
	RemoteUserID string
	Username     string
	Balance      *float64
	Frozen       *float64
	Concurrency  *int
}

// upstreamProviderRemoteUsage is deliberately sourced from the upstream's
// account usage endpoint. It must not be calculated from this application's
// billing logs: those logs include local pricing and can change after a relay
// request has completed.
type upstreamProviderRemoteUsage struct {
	Cost30Days *float64
}

type upstreamProviderRemoteGroup struct {
	RemoteID           string
	Name               string
	Description        string
	Platform           string
	SubscriptionType   string
	Rate               float64
	EffectiveRate      *float64
	IsDynamic          bool
	PeakRateEnabled    bool
	PeakRateMultiplier *float64
	PeakStart          string
	PeakEnd            string
	DailyLimitUSD      *float64
	WeeklyLimitUSD     *float64
	MonthlyLimitUSD    *float64
}

type upstreamProviderRemoteKey struct {
	ID      string
	Key     string
	Name    string
	GroupID string
}

type upstreamProviderRemoteClient interface {
	Login(context.Context, upstreamProviderCredentials) (*upstreamProviderRemoteSession, error)
	Refresh(context.Context, upstreamProviderCredentials) (*upstreamProviderRemoteSession, error)
	Profile(context.Context, upstreamProviderRemoteSession) (*upstreamProviderRemoteProfile, error)
	Groups(context.Context, upstreamProviderRemoteSession) ([]upstreamProviderRemoteGroup, error)
	Usage(context.Context, upstreamProviderRemoteSession) (*upstreamProviderRemoteUsage, error)
	CreateKey(context.Context, upstreamProviderRemoteSession, string, string) (*upstreamProviderRemoteKey, error)
	DeleteKey(context.Context, upstreamProviderRemoteSession, string) error
	Models(context.Context, string) ([]string, error)
}

type upstreamProviderHTTPClient struct {
	baseURL string
	client  *http.Client
}

type upstreamProviderHTTPStatusError struct {
	status int
}

func (err *upstreamProviderHTTPStatusError) Error() string {
	return fmt.Sprintf("upstream returned HTTP %d", err.status)
}

type upstreamProviderEnvelope struct {
	Success *bool           `json:"success"`
	Code    *int            `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func newUpstreamProviderHTTPClient(baseURL string) (*upstreamProviderHTTPClient, error) {
	normalized, err := normalizeUpstreamProviderBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	sharedClient := GetSSRFProtectedHTTPClient()
	if sharedClient == nil {
		sharedClient = http.DefaultClient
	}
	client := *sharedClient
	initialURL, err := url.Parse(normalized)
	if err != nil {
		return nil, errors.New("upstream base URL is invalid")
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if request == nil || request.URL == nil || request.URL.User != nil ||
			request.URL.Scheme != initialURL.Scheme ||
			!strings.EqualFold(request.URL.Host, initialURL.Host) {
			return errors.New("upstream redirect crossed its configured origin")
		}
		if err := ValidateSSRFProtectedFetchURL(request.URL.String()); err != nil {
			return errors.New("upstream redirect is blocked")
		}
		return nil
	}
	return &upstreamProviderHTTPClient{baseURL: normalized, client: &client}, nil
}

// NormalizeUpstreamProviderBaseURL checks that an operator-supplied upstream
// target is a normal HTTP(S) site and applies the deployment's SSRF policy.
func NormalizeUpstreamProviderBaseURL(baseURL string) (string, error) {
	return normalizeUpstreamProviderBaseURL(baseURL)
}

func normalizeUpstreamProviderBaseURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed == nil || parsed.Host == "" {
		return "", errors.New("upstream base URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("upstream base URL must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("upstream base URL must not contain credentials, query, or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	normalized := strings.TrimRight(parsed.String(), "/")
	if err := ValidateSSRFProtectedFetchURL(normalized); err != nil {
		return "", fmt.Errorf("upstream base URL is blocked: %w", err)
	}
	return normalized, nil
}

func (client *upstreamProviderHTTPClient) request(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	payload any,
	headers http.Header,
) ([]byte, http.Header, error) {
	requestURL := client.baseURL + path
	if query != nil {
		requestURL += "?" + query.Encode()
	}
	if err := ValidateSSRFProtectedFetchURL(requestURL); err != nil {
		return nil, nil, fmt.Errorf("upstream request URL is blocked: %w", err)
	}

	var body io.Reader
	if payload != nil {
		encoded, err := common.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("encode upstream request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	requestContext, cancel := context.WithTimeout(ctx, upstreamProviderRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, requestURL, body)
	if err != nil {
		return nil, nil, fmt.Errorf("build upstream request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}

	response, err := client.client.Do(request)
	if err != nil {
		return nil, nil, errors.New("upstream is unreachable")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, upstreamProviderMaxBodyBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read upstream response: %w", err)
	}
	if len(responseBody) > upstreamProviderMaxBodyBytes {
		return nil, nil, errors.New("upstream response is too large")
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, nil, errUpstreamProviderUnauthorized
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, nil, &upstreamProviderHTTPStatusError{status: response.StatusCode}
	}
	return responseBody, response.Header, nil
}

func decodeUpstreamPayload(body []byte, target any) error {
	var envelope upstreamProviderEnvelope
	if err := common.Unmarshal(body, &envelope); err != nil {
		return errors.New("upstream returned invalid JSON")
	}
	if envelope.Success != nil && !*envelope.Success {
		return upstreamProviderResponseError(envelope.Message)
	}
	if envelope.Code != nil && *envelope.Code != 0 {
		return upstreamProviderResponseError(envelope.Message)
	}
	payload := envelope.Data
	if len(payload) == 0 || string(payload) == "null" {
		payload = body
	}
	if err := common.Unmarshal(payload, target); err != nil {
		return errors.New("upstream returned an unexpected response")
	}
	return nil
}

func upstreamProviderResponseError(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return errUpstreamProviderRejected
	}
	// Both upstreams can report an expired access token in a successful HTTP
	// response envelope. Preserve this classification so the caller can retry
	// with its stored password instead of treating the snapshot as a permanent
	// failure.
	normalized := strings.ToLower(message)
	for _, marker := range []string{
		"unauthorized",
		"invalid token",
		"token expired",
		"authentication failed",
		"not authenticated",
		"not logged in",
		"未登录",
		"令牌无效",
		"令牌过期",
		"认证失败",
	} {
		if strings.Contains(normalized, marker) {
			return errUpstreamProviderUnauthorized
		}
	}
	return errUpstreamProviderRejected
}

func upstreamBearerHeaders(token string) http.Header {
	headers := make(http.Header)
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	return headers
}

func upstreamCookieHeaders(cookie string) http.Header {
	headers := make(http.Header)
	if cookie != "" {
		headers.Set("Cookie", cookie)
	}
	return headers
}

func upstreamCookieFromHeaders(headers http.Header) string {
	values := headers.Values("Set-Cookie")
	parts := make([]string, 0, len(values))
	for _, value := range values {
		cookie, err := http.ParseSetCookie(value)
		if err != nil || cookie == nil || cookie.Name == "" {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

func upstreamFloat(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	case float32:
		result := float64(number)
		return result, !math.IsNaN(result) && !math.IsInf(result, 0)
	case json.Number:
		result, err := number.Float64()
		return result, err == nil && !math.IsNaN(result) && !math.IsInf(result, 0)
	case string:
		result, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		return result, err == nil && !math.IsNaN(result) && !math.IsInf(result, 0)
	default:
		return 0, false
	}
}

func upstreamString(value any) string {
	switch result := value.(type) {
	case string:
		return strings.TrimSpace(result)
	case float64:
		return strconv.FormatInt(int64(result), 10)
	case json.Number:
		return result.String()
	default:
		return ""
	}
}

func normalizeUpstreamModelIDs(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > 255 {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	return result
}

func (client *upstreamProviderHTTPClient) models(ctx context.Context, key string) ([]string, error) {
	body, _, err := client.request(ctx, http.MethodGet, "/v1/models", nil, nil, upstreamBearerHeaders(key))
	if err != nil {
		return nil, err
	}
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := common.Unmarshal(body, &response); err != nil {
		return nil, errors.New("upstream returned an invalid model list")
	}
	models := make([]string, 0, len(response.Data))
	for _, item := range response.Data {
		models = append(models, item.ID)
	}
	models = normalizeUpstreamModelIDs(models)
	if len(models) == 0 {
		return nil, errors.New("upstream returned no usable models")
	}
	return models, nil
}

type sub2APIUpstreamClient struct {
	*upstreamProviderHTTPClient
}

func newSub2APIUpstreamClient(baseURL string) (*sub2APIUpstreamClient, error) {
	client, err := newUpstreamProviderHTTPClient(baseURL)
	if err != nil {
		return nil, err
	}
	return &sub2APIUpstreamClient{upstreamProviderHTTPClient: client}, nil
}

func (client *sub2APIUpstreamClient) Login(ctx context.Context, credentials upstreamProviderCredentials) (*upstreamProviderRemoteSession, error) {
	body, _, err := client.request(ctx, http.MethodPost, "/api/v1/auth/login", nil, map[string]string{
		"email": credentials.Username, "password": credentials.Password,
	}, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Requires2FA  bool   `json:"requires_2fa"`
		TempToken    string `json:"temp_token"`
	}
	if err := decodeUpstreamPayload(body, &response); err != nil {
		return nil, err
	}
	if response.Requires2FA {
		if strings.TrimSpace(credentials.TOTPSecret) == "" {
			return nil, errUpstreamProviderTOTPRequired
		}
		code, err := totp.GenerateCode(credentials.TOTPSecret, time.Now())
		if err != nil {
			return nil, errors.New("saved upstream TOTP secret is invalid")
		}
		body, _, err = client.request(ctx, http.MethodPost, "/api/v1/auth/login/2fa", nil, map[string]string{
			"temp_token": response.TempToken, "totp_code": code,
		}, nil)
		if err != nil {
			return nil, err
		}
		if err := decodeUpstreamPayload(body, &response); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return nil, errUpstreamProviderUnauthorized
	}
	expiresAt := time.Now().Add(30 * time.Minute)
	if response.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
	}
	return &upstreamProviderRemoteSession{
		Token:        response.AccessToken,
		RefreshToken: response.RefreshToken,
		ExpiresAt:    &expiresAt,
	}, nil
}

func (client *sub2APIUpstreamClient) Refresh(ctx context.Context, credentials upstreamProviderCredentials) (*upstreamProviderRemoteSession, error) {
	if strings.TrimSpace(credentials.Refresh) == "" {
		return nil, errUpstreamProviderUnauthorized
	}
	body, _, err := client.request(ctx, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{
		"refresh_token": credentials.Refresh,
	}, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := decodeUpstreamPayload(body, &response); err != nil {
		return nil, err
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return nil, errUpstreamProviderUnauthorized
	}
	expiresAt := time.Now().Add(30 * time.Minute)
	if response.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
	}
	if response.RefreshToken == "" {
		response.RefreshToken = credentials.Refresh
	}
	return &upstreamProviderRemoteSession{Token: response.AccessToken, RefreshToken: response.RefreshToken, ExpiresAt: &expiresAt}, nil
}

func (client *sub2APIUpstreamClient) Profile(ctx context.Context, session upstreamProviderRemoteSession) (*upstreamProviderRemoteProfile, error) {
	body, _, err := client.request(ctx, http.MethodGet, "/api/v1/user/profile", nil, nil, upstreamBearerHeaders(session.Token))
	if err != nil {
		return nil, err
	}
	var response struct {
		ID            int64   `json:"id"`
		Email         string  `json:"email"`
		Username      string  `json:"username"`
		Balance       float64 `json:"balance"`
		FrozenBalance float64 `json:"frozen_balance"`
		Concurrency   int     `json:"concurrency"`
	}
	if err := decodeUpstreamPayload(body, &response); err != nil {
		return nil, err
	}
	balance := response.Balance
	frozen := response.FrozenBalance
	concurrency := response.Concurrency
	return &upstreamProviderRemoteProfile{
		RemoteUserID: strconv.FormatInt(response.ID, 10),
		Username:     firstUpstreamNonEmpty(response.Username, response.Email),
		Balance:      &balance,
		Frozen:       &frozen,
		Concurrency:  &concurrency,
	}, nil
}

func (client *sub2APIUpstreamClient) Groups(ctx context.Context, session upstreamProviderRemoteSession) ([]upstreamProviderRemoteGroup, error) {
	body, _, err := client.request(ctx, http.MethodGet, "/api/v1/groups/available", nil, nil, upstreamBearerHeaders(session.Token))
	if err != nil {
		return nil, err
	}
	var response []struct {
		ID                 int64    `json:"id"`
		Name               string   `json:"name"`
		Description        string   `json:"description"`
		Platform           string   `json:"platform"`
		SubscriptionType   string   `json:"subscription_type"`
		RateMultiplier     float64  `json:"rate_multiplier"`
		PeakRateEnabled    bool     `json:"peak_rate_enabled"`
		PeakRateMultiplier *float64 `json:"peak_rate_multiplier"`
		PeakStart          string   `json:"peak_start"`
		PeakEnd            string   `json:"peak_end"`
		DailyLimitUSD      *float64 `json:"daily_limit_usd"`
		WeeklyLimitUSD     *float64 `json:"weekly_limit_usd"`
		MonthlyLimitUSD    *float64 `json:"monthly_limit_usd"`
	}
	if err := decodeUpstreamPayload(body, &response); err != nil {
		return nil, err
	}
	// User-specific rate overrides are optional on older Sub2API releases.
	rates := map[string]float64{}
	if rateBody, _, rateErr := client.request(ctx, http.MethodGet, "/api/v1/groups/rates", nil, nil, upstreamBearerHeaders(session.Token)); rateErr != nil {
		var statusErr *upstreamProviderHTTPStatusError
		if !errors.As(rateErr, &statusErr) || statusErr.status != http.StatusNotFound {
			return nil, rateErr
		}
	} else if err := decodeUpstreamPayload(rateBody, &rates); err != nil {
		return nil, err
	}

	groups := make([]upstreamProviderRemoteGroup, 0, len(response))
	for _, group := range response {
		if group.ID <= 0 || strings.TrimSpace(group.Name) == "" || !upstreamValidMultiplier(group.RateMultiplier) {
			continue
		}
		remoteID := strconv.FormatInt(group.ID, 10)
		var effective *float64
		if rate, ok := rates[remoteID]; ok && upstreamValidMultiplier(rate) {
			effective = &rate
		}
		groups = append(groups, upstreamProviderRemoteGroup{
			RemoteID: remoteID, Name: strings.TrimSpace(group.Name), Description: strings.TrimSpace(group.Description), Platform: strings.TrimSpace(group.Platform),
			SubscriptionType: strings.TrimSpace(group.SubscriptionType), Rate: group.RateMultiplier,
			EffectiveRate: effective, PeakRateEnabled: group.PeakRateEnabled,
			PeakRateMultiplier: group.PeakRateMultiplier, PeakStart: group.PeakStart, PeakEnd: group.PeakEnd,
			DailyLimitUSD: group.DailyLimitUSD, WeeklyLimitUSD: group.WeeklyLimitUSD, MonthlyLimitUSD: group.MonthlyLimitUSD,
		})
	}
	return groups, nil
}

func (client *sub2APIUpstreamClient) Usage(ctx context.Context, session upstreamProviderRemoteSession) (*upstreamProviderRemoteUsage, error) {
	body, _, err := client.request(ctx, http.MethodGet, "/api/v1/usage/stats", url.Values{"period": []string{"month"}}, nil, upstreamBearerHeaders(session.Token))
	if err != nil {
		return nil, err
	}
	var response struct {
		TotalActualCost float64 `json:"total_actual_cost"`
	}
	if err := decodeUpstreamPayload(body, &response); err != nil {
		return nil, err
	}
	if !upstreamValidMultiplier(response.TotalActualCost) {
		return nil, errors.New("upstream returned an invalid usage cost")
	}
	cost := response.TotalActualCost
	return &upstreamProviderRemoteUsage{Cost30Days: &cost}, nil
}

func (client *sub2APIUpstreamClient) CreateKey(ctx context.Context, session upstreamProviderRemoteSession, name string, groupID string) (*upstreamProviderRemoteKey, error) {
	parsedGroupID, err := strconv.ParseInt(groupID, 10, 64)
	if err != nil || parsedGroupID <= 0 {
		return nil, errors.New("invalid upstream group ID")
	}
	body, _, err := client.request(ctx, http.MethodPost, "/api/v1/keys", nil, map[string]any{
		"name": name, "group_id": parsedGroupID,
	}, upstreamBearerHeaders(session.Token))
	if err != nil {
		return nil, err
	}
	var response struct {
		ID      int64  `json:"id"`
		Key     string `json:"key"`
		Name    string `json:"name"`
		GroupID *int64 `json:"group_id"`
	}
	if err := decodeUpstreamPayload(body, &response); err != nil {
		return nil, err
	}
	if strings.TrimSpace(response.Key) == "" {
		return nil, errors.New("upstream returned an empty API key")
	}
	if response.ID <= 0 {
		return nil, errors.New("upstream returned an invalid API key ID")
	}
	remoteKey := &upstreamProviderRemoteKey{ID: strconv.FormatInt(response.ID, 10), Key: response.Key, Name: response.Name, GroupID: groupID}
	if response.GroupID != nil {
		remoteKey.GroupID = strconv.FormatInt(*response.GroupID, 10)
	}
	return remoteKey, nil
}

func (client *sub2APIUpstreamClient) DeleteKey(ctx context.Context, session upstreamProviderRemoteSession, keyID string) error {
	parsedKeyID, err := strconv.ParseInt(keyID, 10, 64)
	if err != nil || parsedKeyID <= 0 {
		return errors.New("invalid upstream API key ID")
	}
	_, _, err = client.request(ctx, http.MethodDelete, "/api/v1/keys/"+strconv.FormatInt(parsedKeyID, 10), nil, nil, upstreamBearerHeaders(session.Token))
	return err
}

func (client *sub2APIUpstreamClient) Models(ctx context.Context, key string) ([]string, error) {
	return client.models(ctx, key)
}

type codeGoUpstreamClient struct {
	*upstreamProviderHTTPClient
}

func newCodeGoUpstreamClient(baseURL string) (*codeGoUpstreamClient, error) {
	client, err := newUpstreamProviderHTTPClient(baseURL)
	if err != nil {
		return nil, err
	}
	return &codeGoUpstreamClient{upstreamProviderHTTPClient: client}, nil
}

func (client *codeGoUpstreamClient) Login(ctx context.Context, credentials upstreamProviderCredentials) (*upstreamProviderRemoteSession, error) {
	body, headers, err := client.request(ctx, http.MethodPost, "/api/user/login", nil, map[string]string{
		"username": credentials.Username, "password": credentials.Password,
	}, nil)
	if err != nil {
		return nil, err
	}
	cookie := upstreamCookieFromHeaders(headers)
	if cookie == "" {
		return nil, errors.New("upstream login did not return a session")
	}
	var data map[string]any
	if err := decodeUpstreamPayload(body, &data); err != nil {
		return nil, err
	}
	if required, _ := data["require_2fa"].(bool); required {
		if strings.TrimSpace(credentials.TOTPSecret) == "" {
			return nil, errUpstreamProviderTOTPRequired
		}
		code, err := totp.GenerateCode(credentials.TOTPSecret, time.Now())
		if err != nil {
			return nil, errors.New("saved upstream TOTP secret is invalid")
		}
		body, headers, err = client.request(ctx, http.MethodPost, "/api/user/login/2fa", nil, map[string]string{"code": code}, upstreamCookieHeaders(cookie))
		if err != nil {
			return nil, err
		}
		if refreshedCookie := upstreamCookieFromHeaders(headers); refreshedCookie != "" {
			cookie = refreshedCookie
		}
		if err := decodeUpstreamPayload(body, &data); err != nil {
			return nil, err
		}
	}
	userID := upstreamString(data["id"])
	if userID == "" {
		return nil, errors.New("upstream login did not return a user ID")
	}
	headersForToken := upstreamCookieHeaders(cookie)
	headersForToken.Set("CodeGo-Api-User", userID)
	body, _, err = client.request(ctx, http.MethodGet, "/api/user/token", nil, nil, headersForToken)
	if err != nil {
		return nil, err
	}
	var tokenPayload json.RawMessage
	if err := decodeUpstreamPayload(body, &tokenPayload); err != nil {
		return nil, err
	}
	var token string
	if err := common.Unmarshal(tokenPayload, &token); err != nil {
		var tokenResponse map[string]any
		if err := common.Unmarshal(tokenPayload, &tokenResponse); err != nil {
			return nil, errors.New("upstream returned an invalid management access token")
		}
		token = firstUpstreamNonEmpty(upstreamString(tokenResponse["access_token"]), upstreamString(tokenResponse["token"]))
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("upstream did not return a management access token")
	}
	return &upstreamProviderRemoteSession{Token: token, RemoteUserID: userID}, nil
}

func (client *codeGoUpstreamClient) Refresh(_ context.Context, _ upstreamProviderCredentials) (*upstreamProviderRemoteSession, error) {
	return nil, errUpstreamProviderUnauthorized
}

func (client *codeGoUpstreamClient) managementHeaders(session upstreamProviderRemoteSession) (http.Header, error) {
	if strings.TrimSpace(session.RemoteUserID) == "" {
		return nil, errors.New("CodeGo management access token requires the remote user ID")
	}
	headers := upstreamBearerHeaders(session.Token)
	headers.Set("CodeGo-Api-User", session.RemoteUserID)
	return headers, nil
}

func (client *codeGoUpstreamClient) Profile(ctx context.Context, session upstreamProviderRemoteSession) (*upstreamProviderRemoteProfile, error) {
	headers, err := client.managementHeaders(session)
	if err != nil {
		return nil, err
	}
	body, _, err := client.request(ctx, http.MethodGet, "/api/user/self", nil, nil, headers)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := decodeUpstreamPayload(body, &data); err != nil {
		return nil, err
	}
	quota, ok := upstreamFloat(data["quota"])
	if !ok || quota < 0 {
		return nil, errors.New("upstream returned an invalid quota")
	}
	statusBody, _, err := client.request(ctx, http.MethodGet, "/api/status", nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch upstream quota conversion: %w", err)
	}
	var status map[string]any
	if err := decodeUpstreamPayload(statusBody, &status); err != nil {
		return nil, fmt.Errorf("parse upstream quota conversion: %w", err)
	}
	quotaPerUnit, ok := upstreamFloat(status["quota_per_unit"])
	if !ok || quotaPerUnit <= 0 {
		return nil, errors.New("upstream returned an invalid quota conversion")
	}
	balance := quota / quotaPerUnit
	if math.IsNaN(balance) || math.IsInf(balance, 0) {
		return nil, errors.New("upstream balance conversion overflowed")
	}
	username := firstUpstreamNonEmpty(upstreamString(data["username"]), upstreamString(data["display_name"]))
	return &upstreamProviderRemoteProfile{
		RemoteUserID: firstUpstreamNonEmpty(upstreamString(data["id"]), session.RemoteUserID),
		Username:     username,
		Balance:      &balance,
	}, nil
}

func (client *codeGoUpstreamClient) Groups(ctx context.Context, session upstreamProviderRemoteSession) ([]upstreamProviderRemoteGroup, error) {
	headers, err := client.managementHeaders(session)
	if err != nil {
		return nil, err
	}
	body, _, err := client.request(ctx, http.MethodGet, "/api/user/self/groups", nil, nil, headers)
	if err != nil {
		return nil, err
	}
	var data map[string]map[string]any
	if err := decodeUpstreamPayload(body, &data); err != nil {
		return nil, err
	}
	groups := make([]upstreamProviderRemoteGroup, 0, len(data))
	for remoteID, group := range data {
		remoteID = strings.TrimSpace(remoteID)
		if remoteID == "" {
			continue
		}
		ratio, isStatic := upstreamFloat(group["ratio"])
		description := upstreamString(group["desc"])
		if !isStatic || !upstreamValidMultiplier(ratio) {
			groups = append(groups, upstreamProviderRemoteGroup{RemoteID: remoteID, Name: remoteID, Description: description, Platform: "codego", IsDynamic: true})
			continue
		}
		effective := ratio
		groups = append(groups, upstreamProviderRemoteGroup{
			RemoteID: remoteID, Name: remoteID, Description: description, Platform: "codego", Rate: ratio, EffectiveRate: &effective,
		})
	}
	return groups, nil
}

func (client *codeGoUpstreamClient) Usage(ctx context.Context, session upstreamProviderRemoteSession) (*upstreamProviderRemoteUsage, error) {
	headers, err := client.managementHeaders(session)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	query := url.Values{
		"start_timestamp": []string{strconv.FormatInt(now.AddDate(0, 0, -30).Unix(), 10)},
		"end_timestamp":   []string{strconv.FormatInt(now.Unix(), 10)},
	}
	body, _, err := client.request(ctx, http.MethodGet, "/api/log/self/stat", query, nil, headers)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := decodeUpstreamPayload(body, &data); err != nil {
		return nil, err
	}
	quota, ok := upstreamFloat(data["quota"])
	if !ok || quota < 0 {
		return nil, errors.New("upstream returned an invalid usage quota")
	}
	statusBody, _, err := client.request(ctx, http.MethodGet, "/api/status", nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch upstream quota conversion: %w", err)
	}
	var status map[string]any
	if err := decodeUpstreamPayload(statusBody, &status); err != nil {
		return nil, fmt.Errorf("parse upstream quota conversion: %w", err)
	}
	quotaPerUnit, ok := upstreamFloat(status["quota_per_unit"])
	if !ok || quotaPerUnit <= 0 {
		return nil, errors.New("upstream returned an invalid quota conversion")
	}
	cost := quota / quotaPerUnit
	if !upstreamValidMultiplier(cost) {
		return nil, errors.New("upstream usage conversion overflowed")
	}
	return &upstreamProviderRemoteUsage{Cost30Days: &cost}, nil
}

func (client *codeGoUpstreamClient) CreateKey(ctx context.Context, session upstreamProviderRemoteSession, name string, groupID string) (*upstreamProviderRemoteKey, error) {
	headers, err := client.managementHeaders(session)
	if err != nil {
		return nil, err
	}
	body, _, err := client.request(ctx, http.MethodPost, "/api/token/", nil, map[string]any{
		"name": name, "remain_quota": 0, "expired_time": -1, "unlimited_quota": true,
		"model_limits_enabled": false, "model_limits": "", "allow_ips": "", "group": groupID,
		"cross_group_retry": false,
	}, headers)
	if err != nil {
		return nil, err
	}
	var ignored map[string]any
	if err := decodeUpstreamPayload(body, &ignored); err != nil {
		return nil, err
	}
	query := url.Values{"keyword": []string{name}, "p": []string{"1"}, "size": []string{"100"}}
	body, _, err = client.request(ctx, http.MethodGet, "/api/token/search", query, nil, headers)
	if err != nil {
		return nil, err
	}
	var search struct {
		Items []map[string]any `json:"items"`
	}
	if err := decodeUpstreamPayload(body, &search); err != nil {
		return nil, err
	}
	remoteID := ""
	for _, item := range search.Items {
		if upstreamString(item["name"]) == name {
			remoteID = upstreamString(item["id"])
			break
		}
	}
	if remoteID == "" {
		return nil, errors.New("created upstream API key could not be found")
	}
	body, _, err = client.request(ctx, http.MethodPost, "/api/token/"+url.PathEscape(remoteID)+"/key", nil, nil, headers)
	if err != nil {
		return nil, err
	}
	var revealed map[string]any
	if err := decodeUpstreamPayload(body, &revealed); err != nil {
		return nil, err
	}
	key := upstreamString(revealed["key"])
	if key == "" {
		return nil, errors.New("upstream returned an empty API key")
	}
	return &upstreamProviderRemoteKey{ID: remoteID, Key: key, Name: name, GroupID: groupID}, nil
}

func (client *codeGoUpstreamClient) DeleteKey(ctx context.Context, session upstreamProviderRemoteSession, keyID string) error {
	headers, err := client.managementHeaders(session)
	if err != nil {
		return err
	}
	keyID = strings.TrimSpace(keyID)
	if keyID == "" || len(keyID) > 128 {
		return errors.New("invalid upstream API key ID")
	}
	_, _, err = client.request(ctx, http.MethodDelete, "/api/token/"+url.PathEscape(keyID), nil, nil, headers)
	return err
}

func (client *codeGoUpstreamClient) Models(ctx context.Context, key string) ([]string, error) {
	return client.models(ctx, key)
}

func upstreamValidMultiplier(rate float64) bool {
	return rate >= 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0)
}

func upstreamValidRateCorrection(rate float64) bool {
	return rate > 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0)
}

func firstUpstreamNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
