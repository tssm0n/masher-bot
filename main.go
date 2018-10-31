package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/nlopes/slack"
)

type GameState struct {
	JoinNext     bool
	AlwaysJoin   bool
	Players      []string
	WaitTime     time.Duration
	MinWaitTime  time.Duration
	HasPosession bool
}

func (s *GameState) ShouldJoin() bool {
	if !s.AlwaysJoin {
		shouldJoin := s.JoinNext
		s.JoinNext = false
		return shouldJoin
	}

	return s.AlwaysJoin
}

type MessageDetails struct {
	msg     string
	channel string
	sender  string
	target  string
}

var state = GameState{false, false, nil, DEFAULT_WAIT_TIME, DEFAULT_MIN_WAIT_TIME, false}

var playerRegex = regexp.MustCompile(`^the players are (.*)$`)
var userRegex = regexp.MustCompile(`^<@(.+)>$`)
var possessionRegex = regexp.MustCompile(`^<@(.+)> has the potato$`)
var endingRegex = regexp.MustCompile(`^<@(.+)> is the winner!$`)
var penaltyRegex = regexp.MustCompile(`^naughty <@(.+)>, .*$`)

const DEFAULT_WAIT_TIME = 3000
const DEFAULT_MIN_WAIT_TIME = 1000
const PENALTY_INCREASE = 500
const BETWEEN_ROUND_ADJUSTMENT = 750

func isPrivateMessage(channel string) bool {
	return strings.HasPrefix(channel, "D")
}

func isGameStarting(text string) bool {
	matched, _ := regexp.MatchString(`hot potato starting in [\d\.]+ seconds - message .*`, text)

	return matched
}

func checkPossession(text, me string) bool {
	match := possessionRegex.FindStringSubmatch(text)

	if len(match) == 0 {
		return false
	}

	passedToMe := strings.ToUpper(match[1]) == me

	if !passedToMe && state.HasPosession {
		// Passed to another player
		state.WaitTime -= 250
		if state.WaitTime < state.MinWaitTime {
			state.WaitTime = state.MinWaitTime
		}
	}

	state.HasPosession = passedToMe

	return passedToMe
}

func gameResult(text, me string) int {
	match := endingRegex.FindStringSubmatch(text)

	if len(match) == 0 {
		return 0
	}

	if strings.ToUpper(match[1]) == me {
		return 1
	}
	return -1
}

func gotPenalty(text, me string) bool {
	match := penaltyRegex.FindStringSubmatch(text)

	if len(match) == 0 {
		return false
	}

	wasPenalized := strings.ToUpper(match[1]) == me

	if wasPenalized {
		state.MinWaitTime = state.WaitTime + 250
		state.WaitTime += PENALTY_INCREASE
	}

	return wasPenalized
}

func parsePlayers(text string, details MessageDetails) []string {
	match := playerRegex.FindStringSubmatch(text)

	if len(match) > 0 {
		names := parseNames(strings.Split(match[1], ","))
		result := names[:0]
		for _, name := range names {
			if name != details.target {
				result = append(result, name)
			}
		}
		return result
	}

	return nil
}

func parseNames(names []string) []string {
	result := make([]string, len(names))

	for index, name := range names {
		result[index] = parseName(name)
	}
	return result
}

func parseName(name string) string {
	result := strings.Trim(name, " ")

	match := userRegex.FindStringSubmatch(result)
	if len(match) > 0 {
		result = strings.ToUpper(match[1])
	}

	return result
}

func sendMessageReply(text, channel string, rtm *slack.RTM) {
	rtm.SendMessage(rtm.NewOutgoingMessage(text, channel))
}

func getUserName(id string, api *slack.Client) string {
	user, _ := api.GetUserInfo(id)
	fmt.Printf("User: %s\n", user.ID)
	fmt.Printf("User: %s\n", user.Name)
	fmt.Printf("User: %s\n", user.RealName)
	fmt.Printf("User: %s\n", user.Profile.DisplayName)
	return user.RealName

}

func handlePrivateMessage(details MessageDetails, rtm *slack.RTM) {
	fmt.Println("Handling private message")

	text := strings.ToLower(details.msg)

	switch text {
	case "join next":
		state.JoinNext = true
		sendMessageReply("OK, I'll join the next game", details.channel, rtm)
	case "join all":
		state.AlwaysJoin = true
		sendMessageReply("Sure, I'll join all the games", details.channel, rtm)
	case "stop":
		state.JoinNext = false
		state.AlwaysJoin = false
		sendMessageReply("Not playing anymore.  I'll take my ball and go home", details.channel, rtm)
	case "test":
		sendMessageReply(":thumbsup:", details.channel, rtm)
	case "reset":
		state.AlwaysJoin = false
		state.JoinNext = false
		state.Players = nil
	case "status":
		stateVal, _ := json.Marshal(state)
		sendMessageReply(string(stateVal), details.channel, rtm)
	default:
		sendMessageReply("Available commands are:  `join next`, `join all`, `stop`, `reset`, `test`", details.channel, rtm)
	}
}

func handlePublicMessage(details MessageDetails, rtm *slack.RTM, api *slack.Client) {
	text := strings.ToLower(details.msg)

	if isGameStarting(text) {
		fmt.Printf("Game starting in channel: %s\n", details.channel)
		if state.ShouldJoin() {
			fmt.Println("Joining the game")
			sendMessageReply("join", details.channel, rtm)
			state.WaitTime = DEFAULT_WAIT_TIME
			state.MinWaitTime = DEFAULT_MIN_WAIT_TIME
		}
	} else if players := parsePlayers(text, details); players != nil {
		fmt.Printf("Found players: %s\n", players)
		state.Players = players

		if state.MinWaitTime < DEFAULT_MIN_WAIT_TIME-BETWEEN_ROUND_ADJUSTMENT {
			state.MinWaitTime = DEFAULT_MIN_WAIT_TIME
		} else {
			state.MinWaitTime -= BETWEEN_ROUND_ADJUSTMENT
		}
	} else if checkPossession(text, details.target) || gotPenalty(text, details.target) {
		fmt.Println("I have the ball!")
		time.Sleep(state.WaitTime * time.Millisecond)

		nextPlayer := rand.Intn(len(state.Players))
		sendMessageReply("pass to <@"+state.Players[nextPlayer]+">", details.channel, rtm)
	} else if result := gameResult(text, details.target); result != 0 {
		if result == 1 {
			fmt.Println("I win!")
			sendMessageReply("Woo hoo! :tada:", details.channel, rtm)
		} else {
			fmt.Println("I lost :(")
			sendMessageReply("Awe man! :cry:", details.channel, rtm)
		}
		state.Players = nil
	} else {
		fmt.Println("Nothing to do")
	}
}

func handleMessageEvent(event *slack.MessageEvent, rtm *slack.RTM, api *slack.Client) {
	info := rtm.GetInfo()

	details := MessageDetails{event.Text, event.Channel, event.User, info.User.ID}

	fmt.Printf("Text: %s\n", details.msg)
	if isPrivateMessage(details.channel) {
		handlePrivateMessage(details, rtm)
	} else {
		handlePublicMessage(details, rtm, api)
	}
}

func main() {
	rand.Seed(time.Now().Unix())

	token := os.Getenv("SLACK_TOKEN")
	api := slack.New(token)
	rtm := api.NewRTM()
	go rtm.ManageConnection()

Loop:
	for {
		select {
		case msg := <-rtm.IncomingEvents:
			// fmt.Println("Event Received: ")
			switch ev := msg.Data.(type) {
			case *slack.ConnectedEvent:
				fmt.Println("Connection counter:", ev.ConnectionCount)

			case *slack.MessageEvent:
				handleMessageEvent(ev, rtm, api)

			case *slack.RTMError:
				fmt.Printf("Error: %s\n", ev.Error())

			case *slack.InvalidAuthEvent:
				fmt.Printf("Invalid credentials")
				break Loop

			default:
				//Take no action
			}
		}
	}
}
