-- Appeals feature schema (MySQL)
-- Charset/collation chosen to support full Unicode

-- Guild-level configuration for appeals
CREATE TABLE IF NOT EXISTS `appeals_settings` (
  `guild_id` VARCHAR(32) NOT NULL,
  `message` TEXT NOT NULL,
  `channel_id` VARCHAR(32) NOT NULL,
  PRIMARY KEY (`guild_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- User appeal submissions
-- status: 0=pending, 1=accepted, 2=rejected
CREATE TABLE IF NOT EXISTS `appeals` (
  `id` VARCHAR(32) NOT NULL,
  `guild_id` VARCHAR(32) NOT NULL,
  `user_id` VARCHAR(32) NOT NULL,
  `content` TEXT NOT NULL,
  `status` TINYINT NOT NULL DEFAULT 0,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `resolved_at` TIMESTAMP NULL DEFAULT NULL,
  `resolved_by` VARCHAR(32) NULL DEFAULT NULL,
  `review_message_id` VARCHAR(32) NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_appeals_guild_id` (`guild_id`),
  KEY `idx_appeals_user_id` (`user_id`),
  KEY `idx_appeals_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


