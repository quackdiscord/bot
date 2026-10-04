package discord

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

// commandMentions maps each application's runnable command paths, such as
// "case view", to the command IDs Discord assigned. Command sync writes it;
// message rendering only reads it. It is keyed by application like the
// emoji catalog, so two bots in one process never borrow each other's IDs.
var commandMentions = struct {
	sync.RWMutex
	applications map[string]map[string]string
}{applications: map[string]map[string]string{}}

// SetCommandMentions replaces applicationID's paths with those of commands,
// the list Discord returned, dropping IDs left over from earlier syncs.
func SetCommandMentions(applicationID string, commands []*discordgo.ApplicationCommand) {
	paths := map[string]string{}
	for _, command := range commands {
		addCommandPaths(paths, command, "")
	}
	commandMentions.Lock()
	defer commandMentions.Unlock()
	commandMentions.applications[applicationID] = paths
}

// RegisterCommandMentions records the ID Discord gave command after a sync
// wrote or confirmed it. Subcommands share their root command's ID.
func RegisterCommandMentions(applicationID string, command *discordgo.ApplicationCommand, commandID string) {
	if command == nil || applicationID == "" || commandID == "" {
		return
	}
	commandMentions.Lock()
	defer commandMentions.Unlock()
	paths := commandMentions.applications[applicationID]
	if paths == nil {
		paths = map[string]string{}
		commandMentions.applications[applicationID] = paths
	}
	removeCommandPaths(paths, command.Name)
	addCommandPaths(paths, command, commandID)
}

// RemoveCommandMentions forgets a command once Discord confirms it was
// deleted.
func RemoveCommandMentions(applicationID, name string) {
	commandMentions.Lock()
	defer commandMentions.Unlock()
	removeCommandPaths(commandMentions.applications[applicationID], name)
}

// removeCommandPaths removes root and its subcommands, but not commands that
// merely share a prefix.
func removeCommandPaths(paths map[string]string, root string) {
	for path := range paths {
		if path == root || strings.HasPrefix(path, root+" ") {
			delete(paths, path)
		}
	}
}

// addCommandPaths records every path of command that can actually be run:
// slash commands only, and never a bare subcommand group. An empty id uses
// the command's own ID.
func addCommandPaths(paths map[string]string, command *discordgo.ApplicationCommand, id string) {
	if command == nil || (command.Type != 0 && command.Type != discordgo.ChatApplicationCommand) {
		return
	}
	if id == "" {
		id = command.ID
	}
	if id == "" || command.Name == "" {
		return
	}
	var walk func(string, []*discordgo.ApplicationCommandOption)
	walk = func(path string, options []*discordgo.ApplicationCommandOption) {
		leaf := true
		for _, option := range options {
			if option != nil && (option.Type == discordgo.ApplicationCommandOptionSubCommand ||
				option.Type == discordgo.ApplicationCommandOptionSubCommandGroup) {
				leaf = false
				walk(path+" "+option.Name, option.Options)
			}
		}
		if leaf {
			paths[path] = id
		}
	}
	walk(command.Name, command.Options)
}

// ResolveCommandMentions turns references to known commands, written as
// /case view or `/case view case:42`, into Discord's clickable command
// mentions without any request. Arguments stay as inline code after the
// mention, since a mention can only hold the path. Unknown commands, code
// blocks, URLs, existing mentions, and escaped inline code stay literal.
func ResolveCommandMentions(content, applicationID string) string {
	commandMentions.RLock()
	ids := maps.Clone(commandMentions.applications[applicationID])
	commandMentions.RUnlock()
	if len(ids) == 0 {
		return content
	}
	paths := slices.Collect(maps.Keys(ids))
	// Longest first, so "/case history list" wins over "/case history".
	slices.SortFunc(paths, func(a, b string) int { return cmp.Compare(len(b), len(a)) })

	var out strings.Builder
	for index := 0; index < len(content); {
		rest := content[index:]
		switch {
		case strings.HasPrefix(rest, "\\`"):
			// Escaped inline code is member text; copy it untouched.
			end := strings.Index(rest[2:], "\\`")
			if end < 0 {
				out.WriteString(rest)
				return out.String()
			}
			out.WriteString(rest[:end+4])
			index += end + 4
		case rest[0] == '`':
			width := 1
			for width < len(rest) && rest[width] == '`' {
				width++
			}
			end := strings.Index(rest[width:], strings.Repeat("`", width))
			if end < 0 {
				out.WriteString(rest)
				return out.String()
			}
			inside := rest[width : width+end]
			replacement, consumed := matchCommandMention(inside, paths, ids)
			if width == 1 && consumed > 0 {
				out.WriteString(replacement)
				if tail := strings.TrimSpace(inside[consumed:]); tail != "" {
					out.WriteString(" `" + tail + "`")
				}
			} else {
				out.WriteString(rest[:width+end+width])
			}
			index += width + end + width
		case rest[0] == '<' && strings.IndexByte(rest, '>') >= 0:
			end := strings.IndexByte(rest, '>') + 1
			out.WriteString(rest[:end])
			index += end
		case rest[0] == '/' && (index == 0 || strings.ContainsRune(" \t\n([\"'", rune(content[index-1]))):
			if replacement, consumed := matchCommandMention(rest, paths, ids); consumed > 0 {
				out.WriteString(replacement)
				index += consumed
				continue
			}
			out.WriteByte(rest[0])
			index++
		default:
			out.WriteByte(rest[0])
			index++
		}
	}
	return out.String()
}

// matchCommandMention matches the longest known path at the start of
// content, and only as a whole word, so /case view never matches
// /case viewer. It returns the mention and how many bytes it replaces.
func matchCommandMention(content string, paths []string, ids map[string]string) (string, int) {
	for _, path := range paths {
		prefix := "/" + path
		if !strings.HasPrefix(content, prefix) {
			continue
		}
		if len(content) > len(prefix) {
			next, _ := utf8.DecodeRuneInString(content[len(prefix):])
			if unicode.IsLetter(next) || unicode.IsNumber(next) || unicode.IsMark(next) || next == '-' || next == '_' || next == '/' {
				continue
			}
		}
		return "</" + path + ":" + ids[path] + ">", len(prefix)
	}
	return "", 0
}
