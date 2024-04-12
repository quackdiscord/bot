package structs

type Case struct {
	ID	 string
	UserID	 string
	GuildID	 string
	ModeratorID string
	Reason string
	Type int8 // 0=warn, 1=ban, 2=kick, 3=unban, 4=timeout, 5=messagedelete
	CreatedAt string
}
