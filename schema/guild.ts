import { boolean, datetime, int, mysqlTable, smallint, varchar } from "drizzle-orm/mysql-core";

export const guilds = mysqlTable("guilds", {
    id: varchar("id", { length: 255 }).primaryKey().notNull(),
    name: varchar("name", { length: 255 }).notNull(),
    description: varchar("description", { length: 255 }),
    member_count: int("member_count").notNull().default(0),
    is_premium: boolean("is_premium").notNull().default(false),
    large: boolean("large").notNull().default(false),
    vanity_url: varchar("vanity_url", { length: 255 }),
    joined_at: datetime("joined_at").notNull().default(new Date()),
    owner_id: varchar("owner_id", { length: 255 }).notNull(),
    shard_id: int("shard_id").notNull().default(0),
    banner_url: varchar("banner_url", { length: 255 }),
    icon: varchar("icon", { length: 255 }),
    max_members: int("max_members").notNull().default(0),
    partnered: boolean("partnered").notNull().default(false),
    afk_channel_id: varchar("afk_channel_id", { length: 255 }),
    afk_timeout: int("afk_timeout").notNull().default(0),
    mfa_level: smallint("mfa_level").notNull().default(0),
    nsfw_level: smallint("nsfw_level").notNull().default(0),
    preferred_locale: varchar("preferred_locale", { length: 255 }).default("en-US"),
    rules_channel_id: varchar("rules_channel_id", { length: 255 }),
    system_channel_id: varchar("system_channel_id", { length: 255 })
});
