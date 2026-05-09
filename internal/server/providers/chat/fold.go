package chat

// Fold reduces a slice of [Event]s into a Room.
//
// Events with unknown `type` or missing required fields are skipped —
// the function is forward-compatible with chat-core extensions.
//
// Members fold by last-event-wins per handle: a `member.joined`
// followed by a later `member.left` removes the member; another
// `member.joined` after that re-adds.
//
// Messages preserve insertion order — chat-core writes them
// chronologically.
func Fold(roomID RoomID, events []Event) Room {
	r := Room{ID: roomID}
	memberMap := map[string]Member{}

	for _, ev := range events {
		switch ev.Type {
		case "message":
			if ev.ID == "" || ev.Sender == "" {
				continue
			}
			r.Messages = append(r.Messages, Message{
				ID:            ev.ID,
				Room:          roomID,
				Timestamp:     ev.Timestamp,
				Sender:        ev.Sender,
				SenderMachine: ev.SenderMachine,
				Text:          ev.Text,
				Source:        normalizeSource(ev.Source),
			})
		case "member.joined":
			if ev.Handle == "" {
				continue
			}
			memberMap[ev.Handle] = Member{
				Handle:      ev.Handle,
				Machine:     ev.Machine,
				TmuxSession: ev.TmuxSession,
				JoinedAt:    ev.Timestamp,
			}
		case "member.left":
			if ev.Handle == "" {
				continue
			}
			delete(memberMap, ev.Handle)
		default:
			continue
		}
		if ev.Timestamp.After(r.LastEventAt) {
			r.LastEventAt = ev.Timestamp
		}
	}

	for _, m := range memberMap {
		r.Members = append(r.Members, m)
	}
	return r
}

func normalizeSource(s string) string {
	if s == "" {
		return "internal"
	}
	return s
}
