-- Mod Profiles feature schema (MySQL)
-- Tracks moderator activity and behavioral patterns for admin oversight

-- Core mod profile with cached statistics
-- Most stats are computed from cases/tickets/appeals/notes tables
-- This table caches expensive-to-compute values and stores activity patterns
CREATE TABLE IF NOT EXISTS `mod_profiles` (
    `mod_id` VARCHAR(32) NOT NULL,
    `guild_id` VARCHAR(32) NOT NULL,
    
    -- Cached case counts (updated on case create/delete)
    `total_cases` INT NOT NULL DEFAULT 0,
    `warns` INT NOT NULL DEFAULT 0,
    `bans` INT NOT NULL DEFAULT 0,
    `kicks` INT NOT NULL DEFAULT 0,
    `timeouts` INT NOT NULL DEFAULT 0,
    `message_deletes` INT NOT NULL DEFAULT 0,
    `unbans` INT NOT NULL DEFAULT 0,
    
    -- Cached ticket/appeal/note counts
    `tickets_resolved` INT NOT NULL DEFAULT 0,
    `appeals_handled` INT NOT NULL DEFAULT 0,
    `notes_created` INT NOT NULL DEFAULT 0,
    
    -- Activity timestamps
    `first_action_at` TIMESTAMP NULL DEFAULT NULL,
    `last_action_at` TIMESTAMP NULL DEFAULT NULL,
    `days_active` INT NOT NULL DEFAULT 0,
    
    -- Streak tracking
    `current_streak_days` INT NOT NULL DEFAULT 0,
    `longest_streak_days` INT NOT NULL DEFAULT 0,
    `last_active_date` DATE NULL DEFAULT NULL,
    
    -- Activity distribution (JSON arrays for hour/weekday counts)
    -- actions_by_hour: [24 integers] for hours 0-23 UTC
    -- actions_by_weekday: [7 integers] for Sunday(0) to Saturday(6)
    `actions_by_hour` JSON NOT NULL DEFAULT ('[]'),
    `actions_by_weekday` JSON NOT NULL DEFAULT ('[]'),
    
    -- Pattern metrics (updated periodically or on profile view)
    `unique_users_punished` INT NOT NULL DEFAULT 0,
    `repeat_offender_ratio` DECIMAL(5,4) NOT NULL DEFAULT 0.0000,
    `single_user_max_cases` INT NOT NULL DEFAULT 0,
    `reason_detail_score` DECIMAL(3,2) NOT NULL DEFAULT 0.00,
    
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    
    PRIMARY KEY (`mod_id`, `guild_id`),
    KEY `idx_mod_profiles_guild_id` (`guild_id`),
    KEY `idx_mod_profiles_total_cases` (`total_cases`),
    KEY `idx_mod_profiles_last_action` (`last_action_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Flags for admin review of moderator behavior
-- type: "low_reason_detail", "high_repeat_target", "unusual_hours", "activity_spike", etc.
-- severity: "info", "warning", "alert"
CREATE TABLE IF NOT EXISTS `mod_flags` (
    `id` VARCHAR(32) NOT NULL,
    `mod_id` VARCHAR(32) NOT NULL,
    `guild_id` VARCHAR(32) NOT NULL,
    `type` VARCHAR(64) NOT NULL,
    `severity` VARCHAR(16) NOT NULL DEFAULT 'info',
    `label` VARCHAR(255) NOT NULL,
    `value` VARCHAR(255) NULL DEFAULT NULL,
    `acknowledged` TINYINT NOT NULL DEFAULT 0,
    `acknowledged_by` VARCHAR(32) NULL DEFAULT NULL,
    `acknowledged_at` TIMESTAMP NULL DEFAULT NULL,
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    KEY `idx_mod_flags_mod_guild` (`mod_id`, `guild_id`),
    KEY `idx_mod_flags_severity` (`severity`),
    KEY `idx_mod_flags_acknowledged` (`acknowledged`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Admin notes about moderators (separate from user notes)
-- For admins to leave private notes about mod performance
CREATE TABLE IF NOT EXISTS `mod_notes` (
    `id` VARCHAR(32) NOT NULL,
    `mod_id` VARCHAR(32) NOT NULL,
    `guild_id` VARCHAR(32) NOT NULL,
    `author_id` VARCHAR(32) NOT NULL,
    `content` TEXT NOT NULL,
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    KEY `idx_mod_notes_mod_guild` (`mod_id`, `guild_id`),
    KEY `idx_mod_notes_author` (`author_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Index on cases table for moderator queries (add if not exists)
-- ALTER TABLE `cases` ADD KEY `idx_cases_moderator_id` (`moderator_id`);

-- Useful queries for computing stats from existing tables:

-- Get case counts by type for a moderator
-- SELECT type, COUNT(*) as count FROM cases WHERE moderator_id = ? AND guild_id = ? GROUP BY type;

-- Get cases in time windows
-- SELECT COUNT(*) FROM cases WHERE moderator_id = ? AND guild_id = ? AND created_at > DATE_SUB(NOW(), INTERVAL 24 HOUR);
-- SELECT COUNT(*) FROM cases WHERE moderator_id = ? AND guild_id = ? AND created_at > DATE_SUB(NOW(), INTERVAL 7 DAY);
-- SELECT COUNT(*) FROM cases WHERE moderator_id = ? AND guild_id = ? AND created_at > DATE_SUB(NOW(), INTERVAL 30 DAY);

-- Get top reasons (requires text analysis, simplified version)
-- SELECT reason, COUNT(*) as count FROM cases WHERE moderator_id = ? AND guild_id = ? GROUP BY reason ORDER BY count DESC LIMIT 10;

-- Get tickets resolved by moderator
-- SELECT COUNT(*) FROM tickets WHERE resolved_by = ? AND guild_id = ?;

-- Get appeals handled by moderator
-- SELECT COUNT(*) FROM appeals WHERE resolved_by = ? AND guild_id = ?;

-- Get frequent users punished by moderator
-- SELECT user_id, COUNT(*) as case_count FROM cases WHERE moderator_id = ? AND guild_id = ? GROUP BY user_id ORDER BY case_count DESC LIMIT 10;

-- Get unique users punished
-- SELECT COUNT(DISTINCT user_id) FROM cases WHERE moderator_id = ? AND guild_id = ?;
