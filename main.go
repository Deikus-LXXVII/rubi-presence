// Command presence is the Location plugin for Rubi: your agent can learn whether you are at home, at work
// or at another place you named, and be told when you arrive or leave, without ever getting coordinates.
//
// Your iPhone decides where you are. Shortcuts automations ("When I arrive at Home", "When I leave Work")
// call private web addresses that Rubi gives you; each address means one thing, like "arrived at Home".
// The request carries nothing else, so neither Rubi Gateway nor anything in between learns where you are,
// only that one of your addresses was called.
package main

import (
	"github.com/Deikus-LXXVII/rubi/sdk/rubiplugin"
)

const (
	ID        = "presence"
	kindRead  = ID + ".read"
	kindWatch = ID + ".watch"
)

// PublisherKey is the Rubi-Project plugin signing key (public half).
const PublisherKey = "MCowBQYDK2VwAyEAxeDfKAkO77JdARN7Y2jJT3tXw9mN+GqqH8R5mhcxt8c="

const addressHelp = "In the Shortcuts app on your iPhone: Automation > New Automation > Arrive (or Leave) > choose the " +
	"place > Run Immediately > Next > New Blank Automation > add \"Get Contents of URL\", paste the address, tap the " +
	"arrow and set Method to POST. Make one automation per address. Keep the addresses private: anyone who has one " +
	"can report that event."

func manifest() rubiplugin.Manifest {
	return rubiplugin.Manifest{
		ID:          ID,
		Name:        "Location",
		Version:     "v1.0.0",
		Description: "Lets your agent know whether you're at home, at work or another place you name, never your coordinates.",
		Needs: "iPhone Shortcuts automations that tell Rubi when you arrive at or leave your places. Rubi gives you " +
			"an address for each; it needs Rubi Gateway or Tailscale as Rubi's transport.",
		Publisher: rubiplugin.Publisher{Name: "Rubi-Project", Key: PublisherKey,
			URL: "https://github.com/Deikus-LXXVII/rubi-presence"},
		Source:  "https://github.com/Deikus-LXXVII/rubi-presence",
		MinRubi: "v0.6.0",
		Entry:   ID,
		Hooks:   true,
		Actions: []rubiplugin.Action{
			{Kind: kindRead, Title: "See where you are (place names only)", DefaultLevel: rubiplugin.None},
			{Kind: kindWatch, Title: "Be told when you arrive or leave (wakes your agent)", DefaultLevel: rubiplugin.None},
		},
		Events: []rubiplugin.EventType{
			{Type: "arrived"},
			{Type: "left"},
		},
		Config: []rubiplugin.ConfigField{
			{Key: "places", Label: "Your places", Type: "list", Default: []string{"Home", "Work"},
				Help: "One per line. The first one is your home. Each gets two addresses below, for arriving and leaving."},
			{Key: "share", Label: "What your agent may know", Type: "choice", Default: "places",
				Options: []rubiplugin.Option{
					{Key: "places", Label: "Which of my places I'm at"},
					{Key: "home", Label: "Only whether I'm at home"},
				}},
			{Key: "addresses", Label: "Addresses for iPhone Shortcuts", Type: "info", Dynamic: true, Help: addressHelp},
		},
	}
}

func main() { newPlugin(&presence{}).Main() }
