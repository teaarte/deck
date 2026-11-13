package gateway

// Facts are what deck knows of a call when it decides whether it may be made: what it needs, what the module
// declared and what the owner answered.
type Facts struct {
	// Owner marks the owner's own call, or one the owner's yes makes as the owner.
	Owner bool `json:"owner"`
	// Needs says the call needs a right of deck's catalog; without one it stays in deck.
	Needs bool `json:"needs"`
	// Cleared marks a call the owner's answer to its ask let through, or the one tool call of a command let through.
	Cleared bool `json:"cleared"`
	// Refused is the command's own refusal of the input, before anyone is asked.
	Refused string `json:"refused"`
	// Draft marks a call of a draft module or of a trial run, which holds no answer of the owner.
	Draft bool `json:"draft"`
	// Looks marks a call whose subject deck checks again when the owner answers, as a tool's.
	Looks bool `json:"looks"`
	// Declared says the module declared the right on this subject, or the command is deck's, whose words deck writes.
	Declared bool `json:"declared"`
	// Answer is what the owner answered to the right on this subject: always, work for this process, or no.
	Answer string `json:"answer"`
	// Pressed says the owner's press named this very call for the run that makes it.
	Pressed bool `json:"pressed"`
	// Asked marks a need only the owner's answer to the ask of this very call meets: no always, press or answer for the work.
	Asked bool `json:"asked"`
	// InForce says what the call would make is so already.
	InForce bool `json:"in_force"`
	// Shown marks a call that, made without the owner, is shown after: it acts in the owner's name, or deck can take
	// it back.
	Shown bool `json:"shown"`
	// Marked says the turn that calls read text from outside deck and holds something of value.
	Marked bool `json:"marked"`
	// Exit says the right is a way out whose subject the call chooses: a network host it does not name, money, an act
	// in the owner's name, another module.
	Exit bool `json:"exit"`
	// Instructs says the call rewrites what deck runs: an instruction of an enabled module or of the workspace.
	Instructs bool `json:"instructs"`
	// Over says the run has spent the most its module declares of the money the call spends: always covers no more.
	Over bool `json:"over"`
}

// The answers of the owner a decision reads beside always and no: for the work of one process.
const Work = "work"

// What a row of the table does with a call.
const (
	DoRun       = "run"
	DoShow      = "show"
	DoRefuse    = "refuse"
	DoUnchanged = "unchanged"
	DoAsk       = "ask"
)

// Row is a line of the table of decisions: its name, what deck does with the call, and why in deck's words.
type Row struct {
	Name string `json:"row"`
	Does string `json:"does"`
	Text string `json:"text"`
}

// decide is the one place deck decides whether a call may be made now, by the table of decisions; every call of a
// caller other than the owner, a tool of an agent's provider among them, goes by it.
func decide(f Facts) Row {
	made := DoRun
	if f.Shown {
		made = DoShow
	}
	switch {
	case f.Owner:
		return Row{"owner", DoRun, "the owner's own call"}
	case f.Marked && f.Instructs && !f.Cleared:
		return Row{"instructs", DoAsk, "this turn read text from outside deck and holds something of value: it changes what an enabled module runs only with the owner's yes to this very change"}
	case !f.Needs:
		return Row{"in_deck", DoRun, "it needs no right of deck's catalog: it stays in deck"}
	case f.Cleared:
		return Row{"cleared", DoRun, "the owner's answer to its request, or the command it is the tool of, let it through"}
	case f.Refused != "":
		return Row{"refused", DoRefuse, f.Refused}
	case f.Draft && !f.Looks:
		return Row{"draft", DoRefuse, "a draft or a trial run holds no answer of the owner, so it was declined"}
	case f.Draft:
		return Row{"draft_tool", DoAsk, "a tool of a draft or a trial run: the owner answers each call"}
	case !f.Declared:
		return Row{"undeclared", DoRefuse, "the module did not declare this right, so it cannot even ask for it"}
	case f.Answer == "no":
		return Row{"denied", DoRefuse, "the owner said no to this right, so it was not made and is not asked again"}
	case f.Marked && f.Exit && !f.Pressed && (f.Answer == "always" || f.Answer == Work):
		return Row{"marked", DoAsk, "this turn read text from outside deck and holds something of value: a way out goes only with the owner's yes to this very call"}
	case f.Over && f.Answer == "always" && !f.Pressed:
		return Row{"over", DoAsk, "this run has spent the most its module declares per run of this money: a call beyond it goes only with the owner's yes to this very call"}
	case f.Pressed && !f.Asked && (f.Marked && f.Exit || f.Over):
		return Row{"pressed", made, "the owner's press named this very call"}
	case f.Answer == "always" && !f.Asked && !f.Over:
		return Row{"always", made, "the owner allows this right always"}
	case f.Answer == Work && !f.Asked:
		return Row{"for_work", made, "the owner allowed this right for the work of this process"}
	case f.Pressed && !f.Asked:
		return Row{"pressed", made, "the owner's press named this very call"}
	case f.InForce:
		return Row{"in_force", DoUnchanged, "what it would make is so already, so nobody is asked"}
	}
	return Row{"ask", DoAsk, "the owner has not answered for this right: deck asks them"}
}
