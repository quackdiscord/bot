package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

// create a new ticket
func CreateTicket(t *structs.Ticket) error {

	// add ticket data to redis active_tickets hash -> thread_id:id
	err := services.Redis.HSet(context.Background(), "active_tickets", t.ThreadID, t.ID).Err()
	if err != nil {
		return err
	}

	// prepare the statement
	stmtIns, err := services.DB.Prepare("INSERT INTO tickets (id, thread_id, owner_id, guild_id, state, log_message_id) VALUES(?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}

	// execute the statement
	_, err = stmtIns.Exec(t.ID, t.ThreadID, t.OwnerID, t.GuildID, t.State, t.LogMessageID)
	if err != nil {
		return err
	}

	return nil

}

// close a ticket
func CloseTicket(id string, threadID string, resolverID string) (*string, error) {

	// store the ticket content
	msgs, err := StoreAllTicketMessages(id)
	if err != nil {
		return nil, err
	}

	// remove ticket data from redis active_tickets hash -> thread_id:id
	err = services.Redis.HDel(context.Background(), "active_tickets", threadID).Err()
	if err != nil {
		// if the error is that the key doesnt exist return nil
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}

	// remove the ticket content from redis
	err = services.Redis.Del(context.Background(), id).Err()
	if err != nil {
		// if the error is that the key doesnt exist return nil
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}

	// prepare the statement
	stmtUpd, err := services.DB.Prepare("UPDATE tickets SET state = 1, resolved_by = ?, resolved_at = ? WHERE id = ?")
	if err != nil {
		return nil, err
	}

	// execute the statement
	_, err = stmtUpd.Exec(resolverID, time.Now(), id)
	if err != nil {
		return nil, err
	}

	return msgs, nil

}

// find a ticket by id
func FindTicketByID(id string) (*structs.Ticket, error) {

	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM tickets WHERE id = ?")
	if err != nil {
		return nil, err
	}

	// query the database
	var t structs.Ticket
	err = stmtOut.QueryRow(id).Scan(&t.ID, &t.ThreadID, &t.OwnerID, &t.GuildID, &t.State, &t.LogMessageID, &t.CreatedAt, &t.ResolvedAt, &t.ResolvedBy, &t.Content)
	if err != nil {
		return nil, err
	}

	return &t, nil

}

// find a ticket by thread id
func FindTicketByThreadID(threadID string) (*structs.Ticket, error) {
	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM tickets WHERE thread_id = ?")
	if err != nil {
		return nil, err
	}
	defer stmtOut.Close()

	// query the database
	var t structs.Ticket
	err = stmtOut.QueryRow(threadID).Scan(&t.ID, &t.ThreadID, &t.OwnerID, &t.GuildID, &t.State, &t.LogMessageID, &t.CreatedAt, &t.ResolvedAt, &t.ResolvedBy, &t.Content)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

// find all open tickets
func FindOpenTickets() ([]*structs.Ticket, error) {

	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM tickets WHERE state = 0")
	if err != nil {
		return nil, err
	}

	// query the database
	rows, err := stmtOut.Query()
	if err != nil {
		return nil, err
	}

	var tickets []*structs.Ticket
	for rows.Next() {
		var t structs.Ticket
		err3 := rows.Scan(&t.ID, &t.ThreadID, &t.OwnerID, &t.GuildID, &t.State, &t.LogMessageID, &t.CreatedAt, &t.ResolvedAt, &t.ResolvedBy, &t.Content)
		if err3 != nil {
			return nil, err3
		}
		tickets = append(tickets, &t)
	}

	return tickets, nil

}

// store a string of messages in db
func StoreAllTicketMessages(ticketID string) (*string, error) {

	// get the list of messages from redis and store them in a string
	messages, err := services.Redis.LRange(context.Background(), ticketID, 0, -1).Result()
	if err != nil {
		// if the error is that the key doesnt exist return nil
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}

	// prepare the statement
	stmtIns, err := services.DB.Prepare("UPDATE tickets SET content = ? WHERE id = ?")
	if err != nil {
		return nil, err
	}

	var msg string
	for _, m := range messages {
		msg += m + "\n"
	}

	// execute the statement
	_, err = stmtIns.Exec(msg, ticketID)
	if err != nil {
		return nil, err
	}

	return &msg, nil

}

// store a single message in redis
func StoreTicketMessage(channelID string, message string, author string) error {

	// get the ticket id from the channel id via redis
	ticketID, err := services.Redis.HGet(context.Background(), "active_tickets", channelID).Result()
	if err != nil {
		// if the error is that the key doesnt exist return nil
		if err == redis.Nil {
			return nil
		}
		return err
	}

	// store the message in redis list (in order of when they were sent so index 0 is the oldest message)
	err = services.Redis.RPush(context.Background(), ticketID, fmt.Sprintf("%s > %s", author, message)).Err()
	if err != nil {
		// if the error is that the key doesn't exist return nil
		if err == redis.Nil {
			return nil
		}
		return err
	}

	return nil

}

// set a guilds ticket channel
func SetTicketChannel(guildID string, channelID string) error { // later gonna change this to all be in Guild object

	// first see if there is a ticket settings object
	curr, err := FindTicketSettingsByGuildID(guildID)
	if err != nil {
		return err
	}

	// if curr is nil, create a new ticket settings object
	if curr == nil {
		t := &structs.TicketSettings{
			GuildID:      guildID,
			ChannelID:    channelID,
			LogChannelID: "",
		}
		err := CreateTicketSettings(t)
		if err != nil {
			return err
		}
	} else {
		// prepare the statement
		stmtUpd, err := services.DB.Prepare("UPDATE ticketsettings SET channel_id = ? WHERE guild_id = ?")
		if err != nil {
			return err
		}

		// execute the statement
		_, err = stmtUpd.Exec(channelID, guildID)
		if err != nil {
			return err
		}

		return nil
	}

	return nil

}

// set a guilds ticket log channel
func SetTicketLogChannel(guildID string, channelID string) error { // later gonna change this to all be in Guild object

	// first see if there is a ticket settings object
	curr, err := FindTicketSettingsByGuildID(guildID)
	if err != nil {
		return err
	}

	// if curr is nil, create a new ticket settings object
	if curr == nil {
		t := &structs.TicketSettings{
			GuildID:      guildID,
			ChannelID:    "",
			LogChannelID: channelID,
		}
		err := CreateTicketSettings(t)
		if err != nil {
			return err
		}
	} else {
		// prepare the statement
		stmtUpd, err := services.DB.Prepare("UPDATE ticketsettings SET log_channel_id = ? WHERE guild_id = ?")
		if err != nil {
			return err
		}

		// execute the statement
		_, err = stmtUpd.Exec(channelID, guildID)
		if err != nil {
			return err
		}

		return nil
	}

	return nil

}

// find a guilds ticket settings
func FindTicketSettingsByGuildID(guildID string) (*structs.TicketSettings, error) {

	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM ticketsettings WHERE guild_id = ?")
	if err != nil {
		return nil, err
	}

	// query the database
	var t structs.TicketSettings
	err = stmtOut.QueryRow(guildID).Scan(&t.GuildID, &t.ChannelID, &t.LogChannelID)
	if err != nil {
		// if the error is that the row doesnt exist return nil
		return nil, nil
	}

	return &t, nil

}

// create a new ticket settings object
func CreateTicketSettings(t *structs.TicketSettings) error {

	// prepare the statement
	stmtIns, err := services.DB.Prepare("INSERT INTO ticketsettings (guild_id, channel_id, log_channel_id) VALUES(?, ?, ?)")
	if err != nil {
		return err
	}

	// execute the statement
	_, err = stmtIns.Exec(t.GuildID, t.ChannelID, t.LogChannelID)
	if err != nil {
		return err
	}

	return nil

}

// get a users ticket
func GetUsersTicket(userID string, guildID string) (*string, error) {

	// prepare the statement
	// find unresolved tickets for the user in the guild
	stmtOut, err := services.DB.Prepare("SELECT thread_id FROM tickets WHERE owner_id = ? AND guild_id = ? AND state = 0")
	if err != nil {
		return nil, err
	}

	// query the database
	var t string
	err = stmtOut.QueryRow(userID, guildID).Scan(&t)
	if err != nil {
		// if the error is that the row doesnt exist return nil
		if err == sql.ErrNoRows {
			return nil, nil
		}

		return nil, err
	}

	return &t, nil

}

// get all open tickets for a guild in order of created at
func GetOpenTickets(guildID string) ([]*structs.Ticket, error) {

	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM tickets WHERE guild_id = ? AND state = 0 ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}

	// query the database
	rows, err := stmtOut.Query(guildID)
	if err != nil {
		return nil, err
	}

	var tickets []*structs.Ticket
	for rows.Next() {
		var t structs.Ticket
		err = rows.Scan(&t.ID, &t.ThreadID, &t.OwnerID, &t.GuildID, &t.State, &t.LogMessageID, &t.CreatedAt, &t.ResolvedAt, &t.ResolvedBy, &t.Content)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, &t)
	}

	return tickets, nil

}
