package agentkit

import (
	"strings"

	"github.com/richardwooding/llmkit/core"
)

// pin is the result of a Pinned tool, remembered so it can be re-sent once a
// compactor has dropped the turn it belongs to.
type pin struct {
	callID  string
	name    string
	content []core.Part
	text    string
}

const pinnedHeading = "Pinned tool results, re-sent after compaction. They remain in effect:"

// collectPins records the successful results of Pinned tools found in msgs.
// Identical content is recorded once so re-activating a tool does not repeat it.
func (r *run) collectPins(msgs []core.Message) {
	for _, m := range msgs {
		for _, tr := range m.ToolResults() {
			r.collectPin(tr)
		}
	}
}

func (r *run) collectPin(tr core.ToolResult) {
	if tr.IsError {
		return
	}
	t, ok := r.agent.tools.Lookup(tr.Name)
	if !ok || !isPinned(t) {
		return
	}
	text := Output{Content: tr.Content}.Text()
	for _, p := range r.pins {
		if p.text == text {
			return
		}
	}
	r.pins = append(r.pins, pin{callID: tr.CallID, name: tr.Name, content: tr.Content, text: text})
}

// visible is the transcript as sent to the model: r.msgs with the content of
// pins whose original result has been compacted away appended to the system
// prompt. r.msgs itself, and so the store, never contain the copy.
func (r *run) visible() []core.Message {
	lost := r.lostPins()
	if len(lost) == 0 {
		return r.msgs
	}
	var b strings.Builder
	b.WriteString(pinnedHeading)
	for _, p := range lost {
		b.WriteString("\n\n")
		b.WriteString(p.text)
	}
	if len(r.msgs) > 0 && r.msgs[0].Role == core.RoleSystem {
		out := make([]core.Message, 0, len(r.msgs))
		out = append(out, core.System(r.msgs[0].Text()+"\n\n"+b.String()))
		return append(out, r.msgs[1:]...)
	}
	out := make([]core.Message, 0, len(r.msgs)+1)
	out = append(out, core.System(b.String()))
	return append(out, r.msgs...)
}

func (r *run) lostPins() []pin {
	if len(r.pins) == 0 {
		return nil
	}
	present := make(map[string]bool, len(r.pins))
	for _, m := range r.msgs {
		for _, tr := range m.ToolResults() {
			present[tr.CallID] = true
		}
	}
	var lost []pin
	for _, p := range r.pins {
		if !present[p.callID] {
			lost = append(lost, p)
		}
	}
	return lost
}
