package structs

// global config for the bot
var Config = &ConfigStruct{
	Hex: 0xfeb032,
}

// ConfigStruct is the global config for the bot

type ConfigStruct struct {
	// Hex is the default color for the bot
	Hex int
}