CREATE TABLE IF NOT EXISTS `honeypots` (
    `id` VARCHAR(32) NOT NULL,
    `guild_id` VARCHAR(32) NOT NULL,
    `action` VARCHAR(32) NOT NULL, 
    `message` TEXT DEFAULT NULL,
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    KEY `idx_honeypots_guild_id` (`guild_id`),
    KEY `idx_honeypots_action` (`action`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;