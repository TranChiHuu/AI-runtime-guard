package main

import (
	"os"

	"github.com/airuntimeguard/core/domain"
)

// Terminal colour for the CLI.
//
// Presentation only: the band comes from the Brain, this file picks the escape
// sequence. Graded rather than always-red on purpose — a score that is red at 25
// and red at 95 has told the developer nothing, and after a week of red they
// stop seeing it.

const (
	reset   = "\x1b[0m"
	boldRed = "\x1b[1;31m"
	red     = "\x1b[31m"
	yellow  = "\x1b[33m"
	dim     = "\x1b[2m"
)

// colourEnabled honours NO_COLOR, the one convention every terminal tool agrees
// on, and disables colour when stdout is not a terminal so a piped `guard
// report` stays greppable.
func colourEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("GUARD_COLOR") == "0" {
		return false
	}
	if os.Getenv("GUARD_COLOR") == "1" {
		return true
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func paint(text, colour string) string {
	if !colourEnabled() {
		return text
	}
	return colour + text + reset
}

// riskScore renders a score coloured by the band it falls in.
func riskScore(score int) string {
	text := padScore(score)
	switch {
	case score >= 80:
		return paint(text, boldRed)
	case score >= 50:
		return paint(text, red)
	case score >= 20:
		return paint(text, yellow)
	default:
		return paint(text, dim)
	}
}

// stateColour matches a session's state to the same scale, so a row reads as
// one thing rather than two competing signals.
func stateColour(s domain.SafetyState) string {
	name := s.String()
	switch s {
	case domain.StateCritical, domain.StateIntervention:
		return paint(name, boldRed)
	case domain.StateWarning:
		return paint(name, red)
	case domain.StateWatching:
		return paint(name, yellow)
	default:
		return paint(name, dim)
	}
}

// actionColour renders a verdict at the severity it carries.
func actionColour(a domain.Action, label string) string {
	switch a {
	case domain.ActionPause, domain.ActionBlock:
		return paint(label, boldRed)
	case domain.ActionAsk:
		return paint(label, red)
	case domain.ActionNotify:
		return paint(label, yellow)
	default:
		return paint(label, dim)
	}
}

// padScore keeps the column aligned without the escape sequences counting
// toward the width, which is what would happen if tabwriter saw them.
func padScore(score int) string {
	switch {
	case score >= 100:
		return "100/100"
	case score >= 10:
		return " " + itoa(score) + "/100"
	default:
		return "  " + itoa(score) + "/100"
	}
}

// padVisible pads to a column width counting only visible characters.
//
// tabwriter cannot do this: it measures bytes, so every escape sequence shifts
// the column and a coloured table comes out ragged.
func padVisible(text string, width int) string {
	visible := 0
	inEscape := false
	for _, r := range text {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			visible++
		}
	}
	for visible < width {
		text += " "
		visible++
	}
	return text
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
