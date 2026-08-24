package connector

import (
	"github.com/disgoorg/disgo/discord"
)

// Entitlement slugs.
//
// These are deliberately stable identifiers rather than display text. Baton
// derives entitlement IDs as "<resourceType>:<resourceID>:<slug>" and grant IDs
// from those, so a slug built out of a display name — as this connector
// previously did, with slugs like "Member of Admins" — silently re-identifies
// every entitlement and grant the moment a server, role, or channel is renamed.
const (
	// guildAccessEntitlement is membership of the server itself.
	guildAccessEntitlement = "access"
	// roleMemberEntitlement is assignment of a role.
	roleMemberEntitlement = "member"
)

// permissionScope records which kinds of channel a permission is meaningful on.
//
// Note that "messaging" is not a voice/text distinction. Since Discord added
// text-in-voice, voice and stage channels carry an integrated text chat, so the
// messaging permissions are settable on their overwrites too. The only
// genuinely text-exclusive permissions are the thread ones, because voice
// channels do not host threads.
type permissionScope int

const (
	// scopeAll applies to every governed channel type.
	scopeAll permissionScope = iota
	// scopeThread applies to channels that host threads: text, announcement,
	// and forum.
	scopeThread
	// scopeVoice applies to voice and stage channels.
	scopeVoice
)

// channelPermission is one governable channel permission.
type channelPermission struct {
	// Slug is the stable entitlement slug. Never derived from display text.
	Slug        string
	DisplayName string
	Description string
	// Value is the Discord permission bit.
	Value discord.Permissions
	Scope permissionScope
}

// channelPermissions is the set of channel-scoped permissions this connector
// governs. Guild-wide permissions (Administrator, ManageGuild, and friends) are
// intentionally excluded: they are not expressible as channel overwrites.
var channelPermissions = []channelPermission{
	{"view_channel", "View Channel", "View the channel", discord.PermissionViewChannel, scopeAll},
	{"manage_channel", "Manage Channel", "Edit and delete the channel", discord.PermissionManageChannels, scopeAll},
	{"manage_permissions", "Manage Permissions", "Change the channel's permission overwrites", discord.PermissionManageRoles, scopeAll},
	{"manage_webhooks", "Manage Webhooks", "Create and edit webhooks in the channel", discord.PermissionManageWebhooks, scopeAll},

	// Messaging permissions apply to voice and stage channels as well, via
	// their built-in text chat.
	{"send_messages", "Send Messages", "Send messages in the channel", discord.PermissionSendMessages, scopeAll},
	{"send_tts_messages", "Send Text-To-Speech Messages", "Send text-to-speech messages in the channel", discord.PermissionSendTTSMessages, scopeAll},
	{"manage_messages", "Manage Messages", "Delete and pin other members' messages", discord.PermissionManageMessages, scopeAll},
	{"embed_links", "Embed Links", "Post links that render a preview", discord.PermissionEmbedLinks, scopeAll},
	{"attach_files", "Attach Files", "Upload files to the channel", discord.PermissionAttachFiles, scopeAll},
	{"read_message_history", "Read Message History", "Read messages sent before joining", discord.PermissionReadMessageHistory, scopeAll},
	{"mention_everyone", "Mention Everyone", "Mention @everyone, @here, and all roles", discord.PermissionMentionEveryone, scopeAll},
	{"use_external_emojis", "Use External Emojis", "Use emojis from other servers", discord.PermissionUseExternalEmojis, scopeAll},
	{"use_external_stickers", "Use External Stickers", "Use stickers from other servers", discord.PermissionUseExternalStickers, scopeAll},
	{"add_reactions", "Add Reactions", "React to messages in the channel", discord.PermissionAddReactions, scopeAll},
	{"use_application_commands", "Use Application Commands", "Use slash and context-menu commands", discord.PermissionUseApplicationCommands, scopeAll},

	// Threads are the one genuinely text-exclusive area: voice and stage
	// channels do not host them.
	{"manage_threads", "Manage Threads", "Manage and delete threads in the channel", discord.PermissionManageThreads, scopeThread},
	{"create_public_threads", "Create Public Threads", "Create public threads in the channel", discord.PermissionCreatePublicThreads, scopeThread},
	{"create_private_threads", "Create Private Threads", "Create private threads in the channel", discord.PermissionCreatePrivateThreads, scopeThread},
	{"send_messages_in_threads", "Send Messages In Threads", "Send messages in the channel's threads", discord.PermissionSendMessagesInThreads, scopeThread},

	{"connect", "Connect", "Connect to the voice channel", discord.PermissionConnect, scopeVoice},
	{"speak", "Speak", "Speak in the voice channel", discord.PermissionSpeak, scopeVoice},
	{"stream", "Stream", "Stream video in the voice channel", discord.PermissionStream, scopeVoice},
	{"priority_speaker", "Priority Speaker", "Be heard more clearly when speaking", discord.PermissionPrioritySpeaker, scopeVoice},
	{"mute_members", "Mute Members", "Mute other members in the voice channel", discord.PermissionMuteMembers, scopeVoice},
	{"deafen_members", "Deafen Members", "Deafen other members in the voice channel", discord.PermissionDeafenMembers, scopeVoice},
	{"move_members", "Move Members", "Move members between voice channels", discord.PermissionMoveMembers, scopeVoice},
	{"use_voice_activity", "Use Voice Activity", "Speak without using push-to-talk", discord.PermissionUseVAD, scopeVoice},
	{"request_to_speak", "Request To Speak", "Request to speak in a stage channel", discord.PermissionRequestToSpeak, scopeVoice},
	// Activities are embedded applications launched inside a voice channel, so
	// Discord classifies this as a voice permission despite the name reading
	// like a general one.
	{"use_embedded_activities", "Use Activities", "Launch embedded activities in the voice channel", discord.PermissionUseEmbeddedActivities, scopeVoice},
}

// channelPermissionsBySlug indexes channelPermissions for provisioning, which
// receives an entitlement slug and has to recover the permission bit.
var channelPermissionsBySlug = func() map[string]channelPermission {
	index := make(map[string]channelPermission, len(channelPermissions))
	for _, permission := range channelPermissions {
		index[permission.Slug] = permission
	}
	return index
}()

// isVoiceChannel reports whether a channel type carries voice permissions.
func isVoiceChannel(channelType discord.ChannelType) bool {
	switch channelType {
	case discord.ChannelTypeGuildVoice, discord.ChannelTypeGuildStageVoice:
		return true
	default:
		return false
	}
}

// isSyncableChannel reports whether a channel type is governed by this
// connector.
//
// Categories are excluded because they hold no members of their own, and
// threads because they inherit their parent channel's overwrites rather than
// carrying independently governable access.
func isSyncableChannel(channelType discord.ChannelType) bool {
	switch channelType {
	case discord.ChannelTypeGuildText,
		discord.ChannelTypeGuildNews,
		discord.ChannelTypeGuildForum,
		discord.ChannelTypeGuildVoice,
		discord.ChannelTypeGuildStageVoice:
		return true
	default:
		return false
	}
}

// permissionAppliesTo reports whether a permission is meaningful on a channel
// of the given type.
func permissionAppliesTo(permission channelPermission, channelType discord.ChannelType) bool {
	switch permission.Scope {
	case scopeAll:
		return true
	case scopeVoice:
		return isVoiceChannel(channelType)
	case scopeThread:
		return !isVoiceChannel(channelType)
	default:
		return false
	}
}

// permissionsForChannel returns the permissions that are meaningful on a
// channel of the given type.
//
// The previous implementation handed text channels the text-only set but voice
// channels the union of text and voice permissions, which surfaced meaningless
// entitlements like "Create Private Threads" on a voice channel.
func permissionsForChannel(channelType discord.ChannelType) []channelPermission {
	permissions := make([]channelPermission, 0, len(channelPermissions))
	for _, permission := range channelPermissions {
		if permissionAppliesTo(permission, channelType) {
			permissions = append(permissions, permission)
		}
	}
	return permissions
}
