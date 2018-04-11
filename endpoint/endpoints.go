// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains variables for all known Discord end points.  All functions
// throughout the Discordgo package use these variables for all connections
// to Discord.  These are all exported and you may modify them if needed.

package endpoint

import "strconv"

// APIVersion is the Discord API version used for the REST and Websocket API.
var APIVersion = "6"

// Known Discord API s.
var (
	Status     = "https://status.discordapp.com/api/v2/"
	Sm         = Status + "scheduled-maintenances/"
	SmActive   = Sm + "active.json"
	SmUpcoming = Sm + "upcoming.json"

	Discord    = "https://discordapp.com/"
	API        = Discord + "api/v" + APIVersion + "/"
	Guilds     = API + "guilds/"
	Channels   = API + "channels/"
	Users      = API + "users/"
	Gateway    = API + "gateway"
	GatewayBot = Gateway + "/bot"
	Webhooks   = API + "webhooks/"

	CDN             = "https://cdn.discordapp.com/"
	CDNAttachments  = CDN + "attachments/"
	CDNAvatars      = CDN + "avatars/"
	CDNIcons        = CDN + "icons/"
	CDNSplashes     = CDN + "splashes/"
	CDNChannelIcons = CDN + "channel-icons/"

	Auth           = API + "auth/"
	Login          = Auth + "login"
	Logout         = Auth + "logout"
	Verify         = Auth + "verify"
	VerifyResend   = Auth + "verify/resend"
	ForgotPassword = Auth + "forgot"
	ResetPassword  = Auth + "reset"
	Register       = Auth + "register"

	Voice        = API + "/voice/"
	VoiceRegions = Voice + "regions"
	VoiceIce     = Voice + "ice"

	Tutorial           = API + "tutorial/"
	TutorialIndicators = Tutorial + "indicators"

	Track        = API + "track"
	Sso          = API + "sso"
	Report       = API + "report"
	Integrations = API + "integrations"

	User               = func(uID string) string { return Users + uID }
	UserAvatar         = func(uID, aID string) string { return CDNAvatars + uID + "/" + aID + ".png" }
	UserAvatarAnimated = func(uID, aID string) string { return CDNAvatars + uID + "/" + aID + ".gif" }
	DefaultUserAvatar  = func(uDiscriminator string) string {
		uDiscriminatorInt, _ := strconv.Atoi(uDiscriminator)
		return CDN + "embed/avatars/" + strconv.Itoa(uDiscriminatorInt%5) + ".png"
	}
	UserSettings      = func(uID string) string { return Users + uID + "/settings" }
	UserGuilds        = func(uID string) string { return Users + uID + "/guilds" }
	UserGuild         = func(uID, gID string) string { return Users + uID + "/guilds/" + gID }
	UserGuildSettings = func(uID, gID string) string { return Users + uID + "/guilds/" + gID + "/settings" }
	UserChannels      = func(uID string) string { return Users + uID + "/channels" }
	UserDevices       = func(uID string) string { return Users + uID + "/devices" }
	UserConnections   = func(uID string) string { return Users + uID + "/connections" }
	UserNotes         = func(uID string) string { return Users + "@me/notes/" + uID }

	Guild                = func(gID string) string { return Guilds + gID }
	GuildChannels        = func(gID string) string { return Guilds + gID + "/channels" }
	GuildMembers         = func(gID string) string { return Guilds + gID + "/members" }
	GuildMember          = func(gID, uID string) string { return Guilds + gID + "/members/" + uID }
	GuildMemberRole      = func(gID, uID, rID string) string { return Guilds + gID + "/members/" + uID + "/roles/" + rID }
	GuildBans            = func(gID string) string { return Guilds + gID + "/bans" }
	GuildBan             = func(gID, uID string) string { return Guilds + gID + "/bans/" + uID }
	GuildIntegrations    = func(gID string) string { return Guilds + gID + "/integrations" }
	GuildIntegration     = func(gID, iID string) string { return Guilds + gID + "/integrations/" + iID }
	GuildIntegrationSync = func(gID, iID string) string { return Guilds + gID + "/integrations/" + iID + "/sync" }
	GuildRoles           = func(gID string) string { return Guilds + gID + "/roles" }
	GuildRole            = func(gID, rID string) string { return Guilds + gID + "/roles/" + rID }
	GuildInvites         = func(gID string) string { return Guilds + gID + "/invites" }
	GuildEmbed           = func(gID string) string { return Guilds + gID + "/embed" }
	GuildPrune           = func(gID string) string { return Guilds + gID + "/prune" }
	GuildIcon            = func(gID, hash string) string { return CDNIcons + gID + "/" + hash + ".png" }
	GuildSplash          = func(gID, hash string) string { return CDNSplashes + gID + "/" + hash + ".png" }
	GuildWebhooks        = func(gID string) string { return Guilds + gID + "/webhooks" }
	GuildAuditLogs       = func(gID string) string { return Guilds + gID + "/audit-logs" }

	Channel                   = func(cID string) string { return Channels + cID }
	ChannelPermissions        = func(cID string) string { return Channels + cID + "/permissions" }
	ChannelPermission         = func(cID, tID string) string { return Channels + cID + "/permissions/" + tID }
	ChannelInvites            = func(cID string) string { return Channels + cID + "/invites" }
	ChannelTyping             = func(cID string) string { return Channels + cID + "/typing" }
	ChannelMessages           = func(cID string) string { return Channels + cID + "/messages" }
	ChannelMessage            = func(cID, mID string) string { return Channels + cID + "/messages/" + mID }
	ChannelMessageAck         = func(cID, mID string) string { return Channels + cID + "/messages/" + mID + "/ack" }
	ChannelMessagesBulkDelete = func(cID string) string { return Channel(cID) + "/messages/bulk-delete" }
	ChannelMessagesPins       = func(cID string) string { return Channel(cID) + "/pins" }
	ChannelMessagePin         = func(cID, mID string) string { return Channel(cID) + "/pins/" + mID }

	GroupIcon = func(cID, hash string) string { return CDNChannelIcons + cID + "/" + hash + ".png" }

	ChannelWebhooks = func(cID string) string { return Channel(cID) + "/webhooks" }
	Webhook         = func(wID string) string { return Webhooks + wID }
	WebhookToken    = func(wID, token string) string { return Webhooks + wID + "/" + token }

	MessageReactionsAll = func(cID, mID string) string { return ChannelMessage(cID, mID) + "/reactions" }
	MessageReactions    = func(cID, mID, eID string) string { return ChannelMessage(cID, mID) + "/reactions/" + eID }
	MessageReaction     = func(cID, mID, eID, uID string) string { return MessageReactions(cID, mID, eID) + "/" + uID }

	Relationships       = func() string { return Users + "@me" + "/relationships" }
	Relationship        = func(uID string) string { return Relationships() + "/" + uID }
	RelationshipsMutual = func(uID string) string { return Users + uID + "/relationships" }

	GuildCreate = API + "guilds"

	Invite = func(iID string) string { return API + "invite/" + iID }

	IntegrationsJoin = func(iID string) string { return API + "integrations/" + iID + "/join" }

	Emoji = func(eID string) string { return API + "emojis/" + eID + ".png" }

	Oauth2          = API + "oauth2/"
	Applications    = Oauth2 + "applications"
	Application     = func(aID string) string { return Applications + "/" + aID }
	ApplicationsBot = func(aID string) string { return Applications + "/" + aID + "/bot" }
)
