package main

import "testing"

func TestCommandMatch(t *testing.T) {

	t.Run("exact matching", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test"}
		matches, _ := cmd.Match("!", "!test")
		if !matches {
			t.Errorf("command %v should match with '!test'", cmd)
		}
	})

	t.Run("fail exact matching if different command", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test"}
		matches, _ := cmd.Match("!", "!cmd")
		if matches {
			t.Errorf("command %v should NOT match with '!cmd'", cmd)
		}
	})

	t.Run("fail exact matching if arguments", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test"}
		matches, _ := cmd.Match("!", "!test something")
		if matches {
			t.Errorf("command %v should NOT match with '!test something'", cmd)
		}
	})

	t.Run("match prefix", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test", HasArgs: true}
		matches, _ := cmd.Match("!", "!test something")
		if !matches {
			t.Errorf("command %v should match with '!test something'", cmd)
		}
	})

	t.Run("don't match prefix if different command", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test", HasArgs: true}
		matches, _ := cmd.Match("!", "!cmd something")
		if matches {
			t.Errorf("command %v should NOT match with '!cmd something'", cmd)
		}
	})

	t.Run("don't match prefix if no argument", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test", HasArgs: true}
		matches, _ := cmd.Match("!", "!test")
		if matches {
			t.Errorf("command %v should NOT match with '!test'", cmd)
		}
	})

	t.Run("don't match prefix if no argument but trailling space", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test", HasArgs: true}
		matches, _ := cmd.Match("!", "!test ")
		if matches {
			t.Errorf("command %v should NOT match with '!test '", cmd)
		}
	})

	t.Run("return the rest of the command", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test", HasArgs: true}
		expected := "something something"
		matches, rest := cmd.Match("!", "!test "+expected)
		if !matches {
			t.Errorf("command %v should match with '!test %s'", cmd, expected)
		}
		if rest != expected {
			t.Errorf("matching command %v with '!test %s' should return '%s'", cmd, expected, expected)
		}
	})

}

func TestCommandFind(t *testing.T) {

	adminID := "admin-id"

	bot := DiscordBot{
		commands: []DiscordCommand{
			{Command: "simple"},
			{Command: "admin", Privileged: true},
			{Command: "args", HasArgs: true},
		},
		adminID: adminID,
		prefix:  "!",
	}

	t.Run("basic matching", func(t *testing.T) {
		cmd, _, err := bot.matchCommand(DiscordMessage{Content: "!simple"})
		if err != nil {
			t.Fatal(err)
		} else if cmd.Command != "simple" {
			t.Errorf("incorrect command match %v for '!simple'", cmd)
		}
	})

	t.Run("admin privilege matching", func(t *testing.T) {
		msg := DiscordMessage{
			Content: "!admin",
			Author:  DiscordMember{ID: adminID},
		}
		cmd, _, err := bot.matchCommand(msg)
		if err != nil {
			t.Fatal(err)
		} else if cmd.Command != "admin" {
			t.Errorf("incorrect command match %v for '!admin'", cmd)
		}
	})

	t.Run("admin privilege rejection", func(t *testing.T) {
		cmd, _, err := bot.matchCommand(DiscordMessage{Content: "!admin"})
		if err != nil {
			t.Fatal(err)
		} else if cmd.Command == "admin" {
			t.Errorf("incorrect command match %v for '!admin', should be rejected", cmd)
		}
	})

	t.Run("return message with arguments", func(t *testing.T) {
		cmd, msg, err := bot.matchCommand(DiscordMessage{Content: "!args a b"})
		if err != nil {
			t.Fatal(err)
		} else if cmd.Command != "args" {
			t.Errorf("incorrect command match %v for '!args'", cmd)
		}
		if !(len(msg.Args) == 2 && msg.Args[0] == "a" && msg.Args[1] == "b") {
			t.Errorf("arguments of '!args' weren't parsed correctly, was %v, should be 'a' and 'b'", msg.Args)
		}
	})

}
