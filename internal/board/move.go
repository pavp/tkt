package board

import "slices"

// Transition is a status a ticket can move to from its current one. NeedsForm marks one whose Jira form
// asks for what the board cannot fill, which it leaves to Jira; Fields are those it can.
type Transition struct {
	ID        string
	To        string
	NeedsForm bool
	Fields    []Field
}

// The kinds of field the board fills: one of a list's options, or a user.
const (
	KindOption = "option"
	KindUser   = "user"
)

type Field struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Options  []Option `json:"options,omitempty"`
}

type Option struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// Value fills Field with an option's or a user's ID.
type Value struct {
	Field string
	Kind  string
	ID    string
}

// User is someone a ticket can be assigned to.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Move is one destination of a ticket's Move menu: InJira opens the ticket in Jira, whose form the board cannot
// fill; Fields, when any, are the form the board fills before moving; otherwise it moves after a confirmation.
type Move struct {
	ID     string  `json:"id"`
	To     string  `json:"to"`
	InJira bool    `json:"in_jira"`
	Fields []Field `json:"fields"`
}

var formKinds = []string{KindOption, KindUser}

// leftToJira reports whether the board must leave tr to Jira's own form, and which fields it fills.
func leftToJira(tr Transition) (bool, []Field) {
	var fields []Field
	for _, f := range tr.Fields {
		switch {
		case slices.Contains(formKinds, f.Kind):
			fields = append(fields, f)
		case f.Required:
			return true, nil
		}
	}
	return tr.NeedsForm, fields
}

// Moves lists each destination once, in Jira's order; one left to Jira is left out when the ticket has no url to open.
func Moves(trs []Transition, url string) []Move {
	var out []Move
	var seen []string
	for _, tr := range trs {
		if slices.Contains(seen, tr.To) {
			continue
		}
		seen = append(seen, tr.To)
		jira, fields := leftToJira(tr)
		if jira {
			if url == "" {
				continue
			}
			fields = nil
		}
		out = append(out, Move{ID: tr.ID, To: tr.To, InJira: jira, Fields: fields})
	}
	return out
}

// FormValues are what a move's form writes, from the option or user ID chosen per field ID; missing is the first
// required field left without one, which moving needs.
func FormValues(fields []Field, chosen map[string]string) (values []Value, missing *Field) {
	for _, f := range fields {
		id := chosen[f.ID]
		if id == "" {
			if f.Required {
				return nil, &f
			}
			continue
		}
		values = append(values, Value{Field: f.ID, Kind: f.Kind, ID: id})
	}
	return values, nil
}
