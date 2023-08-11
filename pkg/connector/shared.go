package connector

import (
	"github.com/bwmarrin/discordgo"
)

var guildPermissions = []int64{
	discordgo.PermissionManageEvents,
	discordgo.PermissionManageEmojis,
	discordgo.PermissionManageNicknames,
	discordgo.PermissionChangeNickname,
	discordgo.PermissionManageRoles,
}

var channelPermissions = append(textChannelPermissions, voiceChannelPermissions...)

var textChannelPermissions = []int64{
	discordgo.PermissionSendMessages,
	discordgo.PermissionSendTTSMessages,
	discordgo.PermissionManageMessages,
	discordgo.PermissionEmbedLinks,
	discordgo.PermissionAttachFiles,
	discordgo.PermissionReadMessageHistory,
	discordgo.PermissionMentionEveryone,
	discordgo.PermissionUseExternalEmojis,
	discordgo.PermissionUseSlashCommands,
	discordgo.PermissionManageThreads,
	discordgo.PermissionCreatePublicThreads,
	discordgo.PermissionCreatePrivateThreads,
	discordgo.PermissionUseExternalStickers,
	discordgo.PermissionSendMessagesInThreads,
	discordgo.PermissionUseActivities,
	discordgo.PermissionManageWebhooks,
}

var voiceChannelPermissions = []int64{
	discordgo.PermissionVoicePrioritySpeaker,
	discordgo.PermissionVoiceStreamVideo,
	discordgo.PermissionVoiceConnect,
	discordgo.PermissionVoiceSpeak,
	discordgo.PermissionVoiceMuteMembers,
	discordgo.PermissionVoiceDeafenMembers,
	discordgo.PermissionVoiceMoveMembers,
	discordgo.PermissionVoiceUseVAD,
	discordgo.PermissionVoiceRequestToSpeak,
}

var permNameFromVal = map[int64]string{
	discordgo.PermissionSendMessages:          "SendMessages",
	discordgo.PermissionSendTTSMessages:       "SendTTSMessages",
	discordgo.PermissionManageMessages:        "ManageMessages",
	discordgo.PermissionEmbedLinks:            "EmbedLinks",
	discordgo.PermissionAttachFiles:           "AttachFiles",
	discordgo.PermissionReadMessageHistory:    "ReadMessageHistory",
	discordgo.PermissionMentionEveryone:       "MentionEveryone",
	discordgo.PermissionUseExternalEmojis:     "UseExternalEmojis",
	discordgo.PermissionUseSlashCommands:      "UseSlashCommands",
	discordgo.PermissionManageThreads:         "ManageThreads",
	discordgo.PermissionCreatePublicThreads:   "CreatePublicThreads",
	discordgo.PermissionCreatePrivateThreads:  "CreatePrivateThreads",
	discordgo.PermissionUseExternalStickers:   "UseExternalStickers",
	discordgo.PermissionSendMessagesInThreads: "SendMessagesInThreads",
	discordgo.PermissionVoicePrioritySpeaker:  "VoicePrioritySpeaker",
	discordgo.PermissionVoiceStreamVideo:      "VoiceStreamVideo",
	discordgo.PermissionVoiceConnect:          "VoiceConnect",
	discordgo.PermissionVoiceSpeak:            "VoiceSpeak",
	discordgo.PermissionVoiceMuteMembers:      "VoiceMuteMembers",
	discordgo.PermissionVoiceDeafenMembers:    "VoiceDeafenMembers",
	discordgo.PermissionVoiceMoveMembers:      "VoiceMoveMembers",
	discordgo.PermissionVoiceUseVAD:           "VoiceUseVAD",
	discordgo.PermissionVoiceRequestToSpeak:   "VoiceRequestToSpeak",
	discordgo.PermissionUseActivities:         "UseActivities",
	discordgo.PermissionManageWebhooks:        "ManageWebhooks",
	discordgo.PermissionManageEvents:          "ManageEvents",
	discordgo.PermissionManageEmojis:          "ManageEmojis",
	discordgo.PermissionManageNicknames:       "ManageNicknames",
	discordgo.PermissionChangeNickname:        "ChangeNickname",
	discordgo.PermissionManageRoles:           "ManageRoles",
}
