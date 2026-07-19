package tui

import (
	"bytes"
	"time"
	"unicode/utf8"
)

type keyKind int

const (
	keyText keyKind = iota
	keyEnter
	keyNewline
	keyTab
	keyEscape
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyPageUp
	keyPageDown
	keyCtrlC
	keyCtrlW
	keyWordLeft
	keyWordRight
	keyFocusIn
	keyFocusOut
)

type key struct {
	kind keyKind
	text string
}

type keyDecoder struct {
	buffer  []byte
	pasting bool
	pending time.Time
}

var keySequences = []struct {
	sequence []byte
	kind     keyKind
}{
	{[]byte("\x1b[3~"), keyDelete},
	{[]byte("\x1b[5~"), keyPageUp},
	{[]byte("\x1b[6~"), keyPageDown},
	{[]byte("\x1b[H"), keyHome},
	{[]byte("\x1b[F"), keyEnd},
	{[]byte("\x1bOH"), keyHome},
	{[]byte("\x1bOF"), keyEnd},
	{[]byte("\x1b[D"), keyLeft},
	{[]byte("\x1b[C"), keyRight},
	{[]byte("\x1b[A"), keyUp},
	{[]byte("\x1b[B"), keyDown},
	{[]byte("\x1b[I"), keyFocusIn},
	{[]byte("\x1b[O"), keyFocusOut},
	{[]byte("\x1bb"), keyWordLeft},
	{[]byte("\x1bf"), keyWordRight},
}

func (d *keyDecoder) feed(data []byte) []key {
	d.buffer = append(d.buffer, data...)
	keys := make([]key, 0)
	for len(d.buffer) > 0 {
		if d.pasting {
			end := bytes.Index(d.buffer, []byte("\x1b[201~"))
			if end < 0 {
				if len(d.buffer) > 6 {
					text := d.buffer[:len(d.buffer)-6]
					keys = append(keys, key{kind: keyText, text: string(text)})
					d.buffer = append([]byte(nil), d.buffer[len(d.buffer)-6:]...)
				}
				break
			}
			if end > 0 {
				keys = append(keys, key{kind: keyText, text: string(d.buffer[:end])})
			}
			d.buffer = d.buffer[end+6:]
			d.pasting = false
			continue
		}
		if bytes.HasPrefix(d.buffer, []byte("\x1b[200~")) {
			d.buffer = d.buffer[6:]
			d.pasting = true
			continue
		}
		matched := false
		for _, candidate := range keySequences {
			if bytes.HasPrefix(d.buffer, candidate.sequence) {
				keys = append(keys, key{kind: candidate.kind})
				d.buffer = d.buffer[len(candidate.sequence):]
				matched = true
				break
			}
		}
		if matched {
			d.pending = time.Time{}
			continue
		}
		if incompleteKeySequence(d.buffer) {
			if d.pending.IsZero() {
				d.pending = time.Now()
			}
			break
		}
		if d.buffer[0] == 0x1b {
			d.pending = time.Time{}
			keys = append(keys, key{kind: keyEscape})
			d.buffer = d.buffer[1:]
			continue
		}
		switch d.buffer[0] {
		case '\r':
			keys = append(keys, key{kind: keyEnter})
			d.buffer = d.buffer[1:]
		case '\n':
			keys = append(keys, key{kind: keyEnter})
			d.buffer = d.buffer[1:]
		case '\t':
			keys = append(keys, key{kind: keyTab})
			d.buffer = d.buffer[1:]
		case 0x03:
			keys = append(keys, key{kind: keyCtrlC})
			d.buffer = d.buffer[1:]
		case 0x04:
			keys = append(keys, key{kind: keyPageDown})
			d.buffer = d.buffer[1:]
		case 0x0e:
			keys = append(keys, key{kind: keyNewline})
			d.buffer = d.buffer[1:]
		case 0x08, 0x7f:
			keys = append(keys, key{kind: keyBackspace})
			d.buffer = d.buffer[1:]
		case 0x15:
			keys = append(keys, key{kind: keyPageUp})
			d.buffer = d.buffer[1:]
		case 0x17:
			keys = append(keys, key{kind: keyCtrlW})
			d.buffer = d.buffer[1:]
		case 0x01:
			keys = append(keys, key{kind: keyHome})
			d.buffer = d.buffer[1:]
		case 0x05:
			keys = append(keys, key{kind: keyEnd})
			d.buffer = d.buffer[1:]
		default:
			if d.buffer[0] < 0x20 {
				d.buffer = d.buffer[1:]
				continue
			}
			if !utf8.FullRune(d.buffer) {
				return keys
			}
			r, size := utf8.DecodeRune(d.buffer)
			keys = append(keys, key{kind: keyText, text: string(r)})
			d.buffer = d.buffer[size:]
		}
	}
	return keys
}

func (d *keyDecoder) flushPending(now time.Time) []key {
	if d.pending.IsZero() || now.Sub(d.pending) < 60*time.Millisecond || len(d.buffer) == 0 {
		return nil
	}
	d.pending = time.Time{}
	if d.buffer[0] != 0x1b {
		return d.feed(nil)
	}
	d.buffer = d.buffer[1:]
	return append([]key{{kind: keyEscape}}, d.feed(nil)...)
}

func incompleteKeySequence(buffer []byte) bool {
	sequences := make([][]byte, 0, len(keySequences)+1)
	sequences = append(sequences, []byte("\x1b[200~"))
	for _, candidate := range keySequences {
		sequences = append(sequences, candidate.sequence)
	}
	for _, sequence := range sequences {
		if len(buffer) < len(sequence) && bytes.HasPrefix(sequence, buffer) {
			return true
		}
	}
	return false
}
