package main

// this file exists to import all sub-packages to ensure
// they are initialized and registered properly

import (
	_ "github.com/quackdiscord/bot/discord/events"
)
