import { Events } from "discord.js";
import { db, kfProducer } from "../bot";
import DBGuild from "../interfaces/DBGuild";
import { guilds } from "../schema/guild";
import { eq } from "drizzle-orm";

async function execute(oldServer: any, newServer: any) {
    // form the data
    const updateData: DBGuild = {
        id: newServer.id,
        name: newServer.name,
        description: newServer.description || undefined,
        member_count: newServer.memberCount,
        is_premium: false,
        large: newServer.large,
        vanity_url: newServer.vanityURLCode || undefined,
        joined_at: new Date(),
        owner_id: newServer.ownerId,
        shard_id: newServer.shardId,
        banner_url: newServer.bannerURL() || undefined,
        icon: newServer.iconURL() || undefined,
        max_members: newServer.maximumMembers || 0,
        partnered: newServer.partnered,
        afk_channel_id: newServer.afkChannelId || undefined,
        afk_timeout: newServer.afkTimeout,
        mfa_level: newServer.mfaLevel,
        nsfw_level: newServer.nsfwLevel,
        preferred_locale: newServer.preferredLocale,
        rules_channel_id: newServer.rulesChannelId || undefined,
        system_channel_id: newServer.systemChannelId || undefined
    };

    // save the server to the database
    try {
        await db.update(guilds).set(updateData).where(eq(guilds.id, newServer.id)).execute();
    } catch (error) {
        //
    }

    let data = {
        type: "server_update",
        oldServer,
        newServer
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildUpdate,
    once: false,
    execute
};

export { data };
