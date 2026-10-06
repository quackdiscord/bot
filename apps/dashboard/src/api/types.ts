import type { components } from "./schema.gen";

type S = components["schemas"];

export type AuthMe = S["ApiAuthMeResponse"];
export type SessionUser = S["ApiSessionUserResponse"];
export type UserGuild = S["QuackUserGuildListItem"];
export type GuildMe = S["ApiGuildMeResponse"];
export type GuildOps = S["ApiGuildOpsResponse"];
export type Settings = S["QuackGuildSettingsResponse"];
export type SettingsInput = S["QuackGuildSettingsInput"];

export type Template = S["QuackTemplateResponse"];
export type TemplateInput = S["QuackTemplateInput"];
export type TemplateLevel = S["QuackTemplateLevelResponse"];
export type TemplateLevelInput = S["QuackTemplateLevelInput"];
export type TemplateAction = S["QuackTemplateActionResponse"];
export type TemplateActionInput = S["QuackTemplateActionInput"];
export type ContextField = S["QuackTemplateContextFieldResponse"];
export type ContextFieldInput = S["QuackTemplateContextFieldInput"];
export type ContextFieldType = S["QuackContextFieldType"];
export type TemplatePolicy = S["QuackTemplatePolicy"];

export type Case = S["QuackCaseResponse"];
export type CaseDetail = S["QuackCaseDetailResponse"];
export type CaseList = S["QuackCaseListResponse"];
export type CaseProfile = S["QuackCaseProfileResponse"];
export type CaseAction = S["QuackCaseActionResponse"];
export type CaseActionDetail = S["QuackCaseActionDetailResponse"];
export type CaseEvent = S["QuackCaseEventResponse"];
export type CaseEvidence = S["QuackCaseEvidenceResponse"];
export type ContextValue = S["QuackCaseContextValueResponse"];
export type ContextValueInput = S["QuackCaseContextValueInput"];
export type SelectedLevel = S["QuackCaseSelectedLevel"];
export type CaseCreateInput = S["ApiCaseCreateRequest"];
export type Validity = S["QuackCaseValidity"];
export type CaseSource = S["QuackCaseSource"];

export type ActionType = S["QuackActionType"];
export type ExecutionStatus = S["QuackActionExecutionStatus"];
export type FailedAction = S["ApiFailedActionResponse"];

export type Appeal = S["QuackAppealResponse"];
export type AppealStatus = S["QuackAppealStatus"];
export type AppealEvent = S["QuackAppealEventResponse"];
export type AppealEventType = S["QuackAppealEventType"];

export type MemberCase = S["QuackMemberCaseDetail"];
export type MemberCaseSummary = S["QuackMemberCaseSummary"];

export type AuditEntry = S["QuackAuditEntryResponse"];
export type AuditResult = S["QuackAuditResult"];
export type AuditSource = S["QuackAuditSource"];
export type Statistics = S["QuackStaffStatistics"];
export type StatBucket = S["QuackStatisticBucket"];

export type TicketSettings = S["TicketsSettings"];
export type TicketStatus = S["TicketsModuleStatus"];
export type Ticket = S["TicketsTicket"];
export type TicketEvent = S["TicketsEvent"];
export type LoggingSettings = S["LoggingSettings"];
export type LoggingSettingsResponse = S["LoggingSettingsResponse"];
export type HoneypotSettings = S["HoneypotSettings"];
export type HoneypotSettingsResponse = S["HoneypotSettingsResponse"];

export type DirectoryUser = S["ApiDirectoryUser"];
export type DirectoryChannel = S["ApiDirectoryChannel"];
export type DirectoryRole = S["ApiDirectoryRole"];
export type ChannelType = S["ApiChannelType"];
