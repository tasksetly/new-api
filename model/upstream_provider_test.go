package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReplaceUpstreamGroupsAndDeleteProviderUnbindsChannels(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM upstream_groups").Error)
	require.NoError(t, DB.Exec("DELETE FROM upstream_providers").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)

	provider := &UpstreamProvider{
		Name:           "upstream-provider-model-test",
		Type:           UpstreamProviderTypeCodeGo,
		BaseURL:        "https://upstream.example",
		RateCorrection: 1,
	}
	require.NoError(t, CreateUpstreamProvider(provider))
	require.NotZero(t, provider.Id)

	effectiveRate := 1.25
	require.NoError(t, ReplaceUpstreamGroups(provider.Id, []UpstreamGroup{
		{RemoteGroupID: "standard", Name: "Standard", RateMultiplier: 1},
		{RemoteGroupID: "vip", Name: "VIP", RateMultiplier: 1.2, EffectiveRateMultiplier: &effectiveRate},
	}))

	providerID := provider.Id
	remoteID := "42"
	remoteGroupID := "standard"
	remoteKeyID := "remote-key"
	rate := 1.5
	costRate := 1.8
	channel := &Channel{
		Name:               "upstream-bound-channel",
		Key:                "sk-test",
		Status:             common.ChannelStatusEnabled,
		Models:             "gpt-4o",
		Group:              "default",
		UpstreamProviderID: &providerID,
		UpstreamRemoteID:   &remoteID,
		UpstreamGroupID:    &remoteGroupID,
		UpstreamKeyID:      &remoteKeyID,
		UpstreamRate:       &rate,
		UpstreamCostRate:   &costRate,
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))

	updatedRate := 1.5
	require.NoError(t, ReplaceUpstreamGroups(provider.Id, []UpstreamGroup{
		{RemoteGroupID: "vip", Name: "VIP updated", RateMultiplier: 1.4, EffectiveRateMultiplier: &updatedRate},
	}))

	groups, err := ListUpstreamGroups(provider.Id)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "vip", groups[0].RemoteGroupID)
	assert.Equal(t, "VIP updated", groups[0].Name)
	require.NotNil(t, groups[0].EffectiveRateMultiplier)
	assert.Equal(t, updatedRate, *groups[0].EffectiveRateMultiplier)

	var staleChannel Channel
	require.NoError(t, DB.First(&staleChannel, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, staleChannel.Status)
	var ability Ability
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.False(t, ability.Enabled)

	require.NoError(t, DeleteUpstreamProvider(provider.Id))

	_, err = GetUpstreamProviderById(provider.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	var groupCount int64
	require.NoError(t, DB.Model(&UpstreamGroup{}).Where("upstream_provider_id = ?", provider.Id).Count(&groupCount).Error)
	assert.Zero(t, groupCount)

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Nil(t, stored.UpstreamProviderID)
	assert.Nil(t, stored.UpstreamRemoteID)
	assert.Nil(t, stored.UpstreamGroupID)
	assert.Nil(t, stored.UpstreamKeyID)
	assert.Nil(t, stored.UpstreamRate)
	assert.Nil(t, stored.UpstreamCostRate)
	assert.Equal(t, "sk-test", stored.Key)
}
