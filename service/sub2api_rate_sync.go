package service

import (
	"context"
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
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

// Sub2API 上游倍率同步
//
// Sub2API 中转暴露 GET /v1/sub2api/billing，用渠道自己的 API Key 鉴权，返回该 Key
// 所在分组的倍率声明。本文件按渠道拉取该声明并写入渠道的 upstream_rate /
// upstream_cost_rate（成本元数据），供管理端展示与成本核算使用；不修改本地分组
// 倍率，因此不影响用户计费。
const (
	sub2APIBillingPath = "/v1/sub2api/billing"

	sub2APIRateSyncRequestTimeout = 10 * time.Second
	sub2APIRateSyncMaxBodyBytes   = 64 * 1024
	sub2APIRateSyncBatchSize      = 100

	// sub2APIRateSyncMaxMultiplier 限制自动写回允许的倍率上限。渠道倍率本身没有
	// 其它上界，而上游是中转服务、可能被配置错误或恶意放大，写回一个荒谬的值会
	// 污染成本核算与展示；100 倍远高于任何合理的转售加价，再高一律拒收。
	sub2APIRateSyncMaxMultiplier = 100.0

	// sub2APIRateSyncRateScale 与 Sub2API 侧写回的精度一致（DECIMAL(10,4)）。
	sub2APIRateSyncRateScale = 10000.0
)

// ErrSub2APIRateSyncUnsupported 表示上游不提供 Sub2API 计费文档（404/405 或返回体
// 不符合该协议）。这类渠道不是同步失败，而是本来就不适用，扫描时单独计数。
var ErrSub2APIRateSyncUnsupported = errors.New("upstream did not return a Sub2API billing document")

// Sub2APIRateSyncResult 描述一次同步的结果，供手动触发接口与定时任务汇总使用。
type Sub2APIRateSyncResult struct {
	ChannelID  int     `json:"channel_id"`
	Rate       float64 `json:"rate"`
	CostRate   float64 `json:"cost_rate"`
	PeakFactor float64 `json:"peak_factor"`
	Error      string  `json:"error,omitempty"`
}

// Sub2APIRateSyncSummary 汇总一轮扫描。
type Sub2APIRateSyncSummary struct {
	Scanned     int                     `json:"scanned"`
	Synced      int                     `json:"synced"`
	Unsupported int                     `json:"unsupported"`
	Failed      int                     `json:"failed"`
	Results     []Sub2APIRateSyncResult `json:"results,omitempty"`
}

type sub2APIBillingDocument struct {
	Object                  string   `json:"object"`
	SchemaVersion           int      `json:"schema_version"`
	BillingScope            string   `json:"billing_scope"`
	GroupRateMultiplier     *float64 `json:"group_rate_multiplier"`
	UserRateMultiplier      *float64 `json:"user_rate_multiplier"`
	ResolvedRateMultiplier  *float64 `json:"resolved_rate_multiplier"`
	PeakRateEnabled         *bool    `json:"peak_rate_enabled"`
	PeakStart               *string  `json:"peak_start"`
	PeakEnd                 *string  `json:"peak_end"`
	PeakRateMultiplier      *float64 `json:"peak_rate_multiplier"`
	AppliedPeakMultiplier   *float64 `json:"applied_peak_multiplier"`
	EffectiveRateMultiplier *float64 `json:"effective_rate_multiplier"`
	Timezone                *string  `json:"timezone"`
	ObservedAt              string   `json:"observed_at"`
}

// GetSub2APIRateSyncChannel 读取渠道，供控制器区分"渠道不存在"与"渠道不支持同步"。
func GetSub2APIRateSyncChannel(id int) (*model.Channel, error) {
	if id <= 0 {
		return nil, errors.New("invalid channel id")
	}
	channel, err := model.GetChannelById(id, true)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("channel not found")
		}
		return nil, err
	}
	return channel, nil
}

// SetChannelSub2APIRateSyncEnabled 开关单个渠道的上游倍率自动同步。
func SetChannelSub2APIRateSyncEnabled(channel *model.Channel, enabled bool) error {
	if channel == nil || channel.Id <= 0 {
		return errors.New("channel is required")
	}
	if enabled && channel.Type != constant.ChannelTypeSub2API {
		return errors.New("only Sub2API channels can sync an upstream rate")
	}
	settings := channel.GetOtherSettings()
	settings.Sub2APIRateSyncEnabled = enabled
	if !enabled {
		settings.Sub2APIRateSyncLastError = ""
	}
	channel.SetOtherSettings(settings)
	return model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).
		Update("settings", channel.OtherSettings).Error
}

// SyncChannelSub2APIRate 拉取单个渠道的上游倍率并写回渠道成本字段。上游不是
// Sub2API 中转时返回 ErrSub2APIRateSyncUnsupported，调用方应视为跳过。
func SyncChannelSub2APIRate(ctx context.Context, channel *model.Channel) (*Sub2APIRateSyncResult, error) {
	rate, costRate, peakFactor, err := ProbeChannelSub2APIRate(ctx, channel)
	if err != nil {
		return nil, err
	}

	settings := channel.GetOtherSettings()
	settings.Sub2APIRateSyncLastTime = common.GetTimestamp()
	settings.Sub2APIRateSyncLastRate = rate
	settings.Sub2APIRateSyncPeakMultiplier = peakFactor
	settings.Sub2APIRateSyncLastError = ""
	channel.SetOtherSettings(settings)

	// settings 由 SetOtherSettings 序列化，与倍率字段在同一条 UPDATE 中落库，避免
	// 展示值与成本值出现跨请求的不一致窗口。
	updates := map[string]any{
		"upstream_rate":      rate,
		"upstream_cost_rate": costRate,
		"settings":           channel.OtherSettings,
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error; err != nil {
		return nil, err
	}
	channel.UpstreamRate = &rate
	channel.UpstreamCostRate = &costRate

	return &Sub2APIRateSyncResult{ChannelID: channel.Id, Rate: rate, CostRate: costRate, PeakFactor: peakFactor}, nil
}

// ProbeChannelSub2APIRate 只读取上游声明，不落库：返回（基础倍率, 写回倍率,
// 探测时刻峰值系数）。
func ProbeChannelSub2APIRate(ctx context.Context, channel *model.Channel) (rate float64, costRate float64, peakFactor float64, err error) {
	if channel == nil || channel.Id <= 0 {
		return 0, 0, 0, errors.New("channel is required")
	}
	if channel.Type != constant.ChannelTypeSub2API {
		return 0, 0, 0, errors.New("channel is not a Sub2API channel")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	if baseURL == "" {
		return 0, 0, 0, errors.New("channel base URL is required")
	}
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return 0, 0, 0, errors.New("channel has no available API key")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, 0, 0, errors.New("channel API key is required")
	}
	return fetchSub2APIRate(ctx, channel, baseURL, key)
}

// SyncEnabledSub2APIRateChannels 扫描所有开启同步的 Sub2API 渠道并逐个同步。
// 单渠道失败只记录，不中断整轮扫描。
func SyncEnabledSub2APIRateChannels(ctx context.Context) Sub2APIRateSyncSummary {
	summary := Sub2APIRateSyncSummary{Results: make([]Sub2APIRateSyncResult, 0)}

	lastID := 0
	for {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		var channels []*model.Channel
		query := model.DB.Model(&model.Channel{}).
			Where("type = ?", constant.ChannelTypeSub2API).
			Order("id asc").
			Limit(sub2APIRateSyncBatchSize)
		if lastID > 0 {
			query = query.Where("id > ?", lastID)
		}
		if err := query.Find(&channels).Error; err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("sub2api rate sync channel query failed: %v", err))
			break
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		for _, channel := range channels {
			if channel == nil {
				continue
			}
			if ctx != nil && ctx.Err() != nil {
				return summary
			}
			if !channel.GetOtherSettings().Sub2APIRateSyncEnabled {
				continue
			}
			summary.Scanned++
			result, err := SyncChannelSub2APIRate(ctx, channel)
			switch {
			case err == nil:
				summary.Synced++
				summary.Results = append(summary.Results, *result)
			case errors.Is(err, ErrSub2APIRateSyncUnsupported):
				summary.Unsupported++
				markSub2APIRateSyncUnsupported(channel)
			default:
				summary.Failed++
				markSub2APIRateSyncFailed(channel, err)
				summary.Results = append(summary.Results, Sub2APIRateSyncResult{ChannelID: channel.Id, Error: err.Error()})
				logger.LogWarn(context.Background(), fmt.Sprintf(
					"sub2api rate sync failed: channel_id=%d channel_name=%s err=%v", channel.Id, channel.Name, err))
			}
		}

		if len(channels) < sub2APIRateSyncBatchSize {
			break
		}
	}
	return summary
}

// markSub2APIRateSyncFailed 只更新失败原因，保留上一次成功写入的倍率，让管理端
// 同时看到"当前成本倍率是多少"和"最近一次同步为什么失败"。
func markSub2APIRateSyncFailed(channel *model.Channel, syncErr error) {
	if channel == nil {
		return
	}
	persistSub2APIRateSyncState(channel, syncErr.Error(), nil)
}

// markSub2APIRateSyncUnsupported 处理上游明确声明"没有这个接口"的情况：此时旧倍率
// 已经不代表任何真实上游，清掉以免被当成仍然有效的成本信息继续使用。
func markSub2APIRateSyncUnsupported(channel *model.Channel) {
	if channel == nil {
		return
	}
	persistSub2APIRateSyncState(channel, "upstream does not expose /v1/sub2api/billing", map[string]any{
		"upstream_rate":      nil,
		"upstream_cost_rate": nil,
	})
}

func persistSub2APIRateSyncState(channel *model.Channel, message string, extraUpdates map[string]any) {
	settings := channel.GetOtherSettings()
	settings.Sub2APIRateSyncLastTime = common.GetTimestamp()
	settings.Sub2APIRateSyncLastError = message
	channel.SetOtherSettings(settings)

	updates := map[string]any{"settings": channel.OtherSettings}
	for column, value := range extraUpdates {
		updates[column] = value
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error; err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf(
			"sub2api rate sync failure state update failed: channel_id=%d err=%v", channel.Id, err))
	}
}

// fetchSub2APIRate 请求上游计费文档并返回（基础倍率, 写回倍率, 探测时刻峰值系数）。
//
// 写回的是 resolved_rate_multiplier，不是 effective_rate_multiplier：effective
// 已经把探测那一刻是否处于高峰时段折算进去了，直接写成静态值会把某一轮探测赶上
// 的峰值（或谷值）永久冻结。因此这里只存基础倍率和"探测时刻的峰值系数"，需要时
// 由展示侧用 base * peak 还原当时的实际倍率。
func fetchSub2APIRate(ctx context.Context, channel *model.Channel, baseURL, key string) (rate float64, costRate float64, peakFactor float64, err error) {
	requestURL := baseURL + sub2APIBillingPath
	if err := ValidateSSRFProtectedFetchURL(requestURL); err != nil {
		return 0, 0, 0, fmt.Errorf("channel base URL is blocked: %w", err)
	}

	requestCtx := ctx
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(requestCtx, sub2APIRateSyncRequestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	applySub2APIHeaderOverride(request, channel)

	client, err := NewProxyHttpClient(channel.GetSetting().Proxy)
	if err != nil {
		return 0, 0, 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, 0, 0, sanitizeSub2APIRateSyncError(err, key)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
		return 0, 0, 0, ErrSub2APIRateSyncUnsupported
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, sub2APIRateSyncMaxBodyBytes+1))
	if err != nil {
		return 0, 0, 0, errors.New("could not read the upstream billing response")
	}
	if len(body) > sub2APIRateSyncMaxBodyBytes {
		return 0, 0, 0, errors.New("upstream billing response is too large")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return 0, 0, 0, fmt.Errorf("upstream billing request returned status %d", response.StatusCode)
	}

	rate, peakFactor, err = parseSub2APIRateDocument(body)
	if err != nil {
		return 0, 0, 0, err
	}
	costRate, err = sub2APIRateSyncCostRate(rate)
	if err != nil {
		return 0, 0, 0, err
	}
	return rate, costRate, peakFactor, nil
}

// applySub2APIHeaderOverride 让渠道自带的请求头覆盖生效，与其它上游取数路径保持一致。
func applySub2APIHeaderOverride(request *http.Request, channel *model.Channel) {
	headerOverride := channel.GetHeaderOverride()
	if len(headerOverride) == 0 {
		return
	}
	for name, value := range headerOverride {
		text, ok := value.(string)
		if !ok {
			continue
		}
		request.Header.Set(strings.TrimSpace(name), text)
	}
}

// parseSub2APIRateDocument 校验上游声明并返回（基础倍率, 探测时刻峰值系数）。
//
// 校验强度对齐 Sub2API 自己的写入端：schema 必须匹配，倍率必须有限且非负，
// resolved 必须等于 user（存在时）否则 group，applied_peak 必须与 observed_at
// 时刻的时段推算一致，effective 必须等于 resolved * applied_peak。任何一项对不上
// 都判为无效响应，宁可不写也不写入无法解释的成本倍率。
func parseSub2APIRateDocument(body []byte) (rate float64, peakFactor float64, err error) {
	var document sub2APIBillingDocument
	if err := common.Unmarshal(body, &document); err != nil {
		return 0, 0, ErrSub2APIRateSyncUnsupported
	}
	if document.Object != "sub2api.key_billing" || document.SchemaVersion != 1 || document.BillingScope != "token" {
		return 0, 0, ErrSub2APIRateSyncUnsupported
	}
	if document.GroupRateMultiplier == nil || document.ResolvedRateMultiplier == nil ||
		document.PeakRateEnabled == nil || document.EffectiveRateMultiplier == nil {
		return 0, 0, errors.New("upstream billing document is incomplete")
	}
	for _, value := range []float64{
		*document.GroupRateMultiplier,
		*document.ResolvedRateMultiplier,
		*document.EffectiveRateMultiplier,
	} {
		if !isValidSub2APIUpstreamMultiplier(value) {
			return 0, 0, errors.New("upstream declared an invalid billing multiplier")
		}
	}
	expectedResolved := *document.GroupRateMultiplier
	if document.UserRateMultiplier != nil {
		if !isValidSub2APIUpstreamMultiplier(*document.UserRateMultiplier) {
			return 0, 0, errors.New("upstream declared an invalid user billing multiplier")
		}
		expectedResolved = *document.UserRateMultiplier
	}
	if !equalSub2APIMultiplier(*document.ResolvedRateMultiplier, expectedResolved) {
		return 0, 0, errors.New("upstream billing multipliers are inconsistent")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, document.ObservedAt)
	if err != nil || observedAt.IsZero() {
		return 0, 0, errors.New("upstream declared an invalid observation time")
	}

	peakFactor, err = sub2APIAppliedPeakFactor(&document, observedAt)
	if err != nil {
		return 0, 0, err
	}
	if !equalSub2APIMultiplier(*document.EffectiveRateMultiplier, *document.ResolvedRateMultiplier*peakFactor) {
		return 0, 0, errors.New("upstream billing multipliers are inconsistent")
	}
	return *document.ResolvedRateMultiplier, peakFactor, nil
}

// sub2APIAppliedPeakFactor 返回 observed_at 时刻实际生效的峰值系数。未启用峰值计费
// 时恒为 1；启用时要求时段、时区、峰值倍率齐全且可解释。
func sub2APIAppliedPeakFactor(document *sub2APIBillingDocument, observedAt time.Time) (float64, error) {
	if document == nil || document.PeakRateEnabled == nil {
		return 0, errors.New("upstream billing document is incomplete")
	}
	if !*document.PeakRateEnabled {
		if document.AppliedPeakMultiplier != nil && !equalSub2APIMultiplier(*document.AppliedPeakMultiplier, 1) {
			return 0, errors.New("upstream billing multipliers are inconsistent")
		}
		return 1, nil
	}
	if document.PeakStart == nil || document.PeakEnd == nil || document.Timezone == nil ||
		document.PeakRateMultiplier == nil || document.AppliedPeakMultiplier == nil {
		return 0, errors.New("upstream peak billing declaration is incomplete")
	}
	location, err := time.LoadLocation(strings.TrimSpace(*document.Timezone))
	if err != nil {
		return 0, errors.New("upstream declared an unknown peak timezone")
	}
	startMinute, startOK := parseSub2APIMinuteOfDay(*document.PeakStart)
	endMinute, endOK := parseSub2APIMinuteOfDay(*document.PeakEnd)
	if !startOK || !endOK || startMinute >= endMinute {
		return 0, errors.New("upstream declared an invalid peak window")
	}
	peakMultiplier := *document.PeakRateMultiplier
	if !isValidSub2APIUpstreamMultiplier(peakMultiplier) {
		return 0, errors.New("upstream declared an invalid peak multiplier")
	}

	local := observedAt.In(location)
	applied := 1.0
	if minuteOfDay := local.Hour()*60 + local.Minute(); minuteOfDay >= startMinute && minuteOfDay < endMinute {
		applied = peakMultiplier
	}
	if !equalSub2APIMultiplier(*document.AppliedPeakMultiplier, applied) {
		return 0, errors.New("upstream peak multiplier does not match its observation time")
	}
	return applied, nil
}

// parseSub2APIMinuteOfDay 解析 "HH:MM"；Sub2API 以当地分钟数界定高峰区间。
func parseSub2APIMinuteOfDay(value string) (int, bool) {
	hourText, minuteText, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found {
		return 0, false
	}
	hour, hourErr := strconv.Atoi(strings.TrimSpace(hourText))
	minute, minuteErr := strconv.Atoi(strings.TrimSpace(minuteText))
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// sub2APIRateSyncCostRate 把上游声明的倍率规整到可写回的精度并做范围检查。
func sub2APIRateSyncCostRate(rate float64) (float64, error) {
	rounded := math.Round(rate*sub2APIRateSyncRateScale) / sub2APIRateSyncRateScale
	if rounded < 0 || rounded > sub2APIRateSyncMaxMultiplier {
		return 0, fmt.Errorf("upstream declared a billing multiplier outside the accepted range 0-%g", sub2APIRateSyncMaxMultiplier)
	}
	return rounded, nil
}

func isValidSub2APIUpstreamMultiplier(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func equalSub2APIMultiplier(left, right float64) bool {
	if math.IsNaN(left) || math.IsNaN(right) || math.IsInf(left, 0) || math.IsInf(right, 0) {
		return false
	}
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-9*scale
}

// sanitizeSub2APIRateSyncError 避免把可能内嵌渠道 Key 的 URL/传输错误透出到
// 管理端响应与审计记录中。
func sanitizeSub2APIRateSyncError(err error, key string) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	message := err.Error()
	if key = strings.TrimSpace(key); key != "" {
		message = strings.ReplaceAll(message, key, "[REDACTED]")
		message = strings.ReplaceAll(message, url.QueryEscape(key), "[REDACTED]")
	}
	return errors.New(message)
}
