-- Cases table schema (MySQL)
-- Mirrors structs.Case with context_url support

CREATE TABLE IF NOT EXISTS `cases` (
  `id` VARCHAR(32) NOT NULL,
  `user_id` VARCHAR(32) NOT NULL,
  `moderator_id` VARCHAR(32) NOT NULL,
  `guild_id` VARCHAR(32) NOT NULL,
  `reason` TEXT NOT NULL,
  `type` TINYINT NOT NULL,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `context_url` TEXT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_cases_guild_id` (`guild_id`),
  KEY `idx_cases_user_id` (`user_id`),
  KEY `idx_cases_type` (`type`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


