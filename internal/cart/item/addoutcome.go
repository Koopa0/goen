package item

// AddOutcome is carried in ?added= back to the page the form was on.
type AddOutcome string

const (
	AddOutcomeAdded       AddOutcome = "added"
	AddOutcomeAdjusted    AddOutcome = "adjusted"
	AddOutcomeUnavailable AddOutcome = "unavailable"
	AddOutcomeUnknown     AddOutcome = "unknown"
	// AddOutcomeFull is another distinct product that would make the order too large
	// for one provider invoice.
	AddOutcomeFull AddOutcome = "full"
)
