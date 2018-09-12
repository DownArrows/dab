package main

import "testing"

func TestCommandMatch(t *testing.T) {

	t.Run("exact matching", func(t *testing.T) {
		cmd := DiscordCommand{
			Command: "test",
			NoArgs:  true,
		}
		matches, _ := cmd.Match("!", "!test")
		if !matches {
			t.Errorf("Command %v should match with '!test'", cmd)
		}
	})

	t.Run("fail exact matching if different command", func(t *testing.T) {
		cmd := DiscordCommand{
			Command: "test",
			NoArgs:  true,
		}
		matches, _ := cmd.Match("!", "!cmd")
		if matches {
			t.Errorf("Command %v should NOT match with '!cmd'", cmd)
		}
	})

	t.Run("fail exact matching if arguments", func(t *testing.T) {
		cmd := DiscordCommand{
			Command: "test",
			NoArgs:  true,
		}
		matches, _ := cmd.Match("!", "!test something")
		if matches {
			t.Errorf("Command %v should NOT match with '!test something'", cmd)
		}
	})

	t.Run("match prefix", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test"}
		matches, _ := cmd.Match("!", "!test something")
		if !matches {
			t.Errorf("Command %v should match with '!test something'", cmd)
		}
	})

	t.Run("don't match prefix if different command", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test"}
		matches, _ := cmd.Match("!", "!cmd something")
		if matches {
			t.Errorf("Command %v should NOT match with '!cmd something'", cmd)
		}
	})

	t.Run("don't match prefix if no argument", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test"}
		matches, _ := cmd.Match("!", "!test")
		if matches {
			t.Errorf("Command %v should NOT match with '!test'", cmd)
		}
	})

	t.Run("don't match prefix if no argument but trailling space", func(t *testing.T) {
		cmd := DiscordCommand{Command: "test"}
		matches, _ := cmd.Match("!", "!test ")
		if matches {
			t.Errorf("Command %v should NOT match with '!test '", cmd)
		}
	})

}
