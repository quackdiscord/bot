package structs

type Guild struct {
	ID              string
	Name            string
	Description     string
	MemberCount     int
	IsPremium       int // 0 = no, 1 = yes
	Large           int // 0 = no, 1 = yes
	VanityURL       string
	JoinedAt        string
	OwnerID         string
	ShardID         int
	BannerURL       string
	Icon            string
	MaxMembers      int
	Partnered       int // 0 = no, 1 = yes
	AFKChannelID    string
	AFKTimeout      int
	MFALevel        int
	NSFWLevel       int
	PerferedLocale  string
	RulesChannelID  string
	SystemChannelID string
}
