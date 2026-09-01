package tui

import (
	"bytes"
	"strconv"
	"strings"
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
	keyScrollUp
	keyScrollDown
	keyCtrlC
	keyToggleMarkdown
	keyCtrlW
	keyWordLeft
	keyWordRight
	keyFocusIn
	keyFocusOut
	keyExternalEditor
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
	{[]byte{0x18, 0x05}, keyExternalEditor},
	{[]byte("\x1bOS"), keyExternalEditor},
	{[]byte("\x1b[14~"), keyExternalEditor},
	{[]byte("\x1b[1;1S"), keyExternalEditor},
	{[]byte("\x1b[27;2;13~"), keyNewline},
	{[]byte("\x1b[3~"), keyDelete},
	{[]byte("\x1b[3;1~"), keyDelete},
	{[]byte("\x1bOQ"), keyToggleMarkdown},
	{[]byte("\x1b[12~"), keyToggleMarkdown},
	{[]byte("\x1b[1;1Q"), keyToggleMarkdown},
	{[]byte("\x1b[5~"), keyPageUp},
	{[]byte("\x1b[5;1~"), keyPageUp},
	{[]byte("\x1b[6~"), keyPageDown},
	{[]byte("\x1b[6;1~"), keyPageDown},
	{[]byte("\x1b[H"), keyHome},
	{[]byte("\x1b[1;1H"), keyHome},
	{[]byte("\x1b[F"), keyEnd},
	{[]byte("\x1b[1;1F"), keyEnd},
	{[]byte("\x1bOH"), keyHome},
	{[]byte("\x1bOF"), keyEnd},
	{[]byte("\x1b[1;5D"), keyWordLeft},
	{[]byte("\x1b[5D"), keyWordLeft},
	{[]byte("\x1b[1;5C"), keyWordRight},
	{[]byte("\x1b[5C"), keyWordRight},
	{[]byte("\x1b[D"), keyLeft},
	{[]byte("\x1b[1;1D"), keyLeft},
	{[]byte("\x1b[C"), keyRight},
	{[]byte("\x1b[1;1C"), keyRight},
	{[]byte("\x1b[A"), keyUp},
	{[]byte("\x1b[1;1A"), keyUp},
	{[]byte("\x1b[B"), keyDown},
	{[]byte("\x1b[1;1B"), keyDown},
	{[]byte("\x1b[I"), keyFocusIn},
	{[]byte("\x1b[O"), keyFocusOut},
	{[]byte("\x1bb"), keyWordLeft},
	{[]byte("\x1bf"), keyWordRight},
	{[]byte("\x1be"), keyExternalEditor},
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
		if parsed, consumed, incomplete := parseCSIUKey(d.buffer); incomplete {
			if d.pending.IsZero() {
				d.pending = time.Now()
			}
			break
		} else if consumed > 0 {
			if parsed != nil {
				keys = append(keys, *parsed)
			}
			d.buffer = d.buffer[consumed:]
			d.pending = time.Time{}
			continue
		}
		if parsed, consumed, incomplete := parseSGRMouse(d.buffer); incomplete {
			if d.pending.IsZero() {
				d.pending = time.Now()
			}
			break
		} else if consumed > 0 {
			if parsed != nil {
				keys = append(keys, *parsed)
			}
			d.buffer = d.buffer[consumed:]
			d.pending = time.Time{}
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
		d.buffer = d.buffer[1:]
		return d.feed(nil)
	}
	d.buffer = d.buffer[1:]
	return append([]key{{kind: keyEscape}}, d.feed(nil)...)
}

func parseCSIUKey(buffer []byte) (*key, int, bool) {
	prefix := []byte("\x1b[")
	if len(buffer) < len(prefix) {
		return nil, 0, bytes.HasPrefix(prefix, buffer)
	}
	if !bytes.HasPrefix(buffer, prefix) {
		return nil, 0, false
	}

	end := -1
	for index, value := range buffer[len(prefix):] {
		if value >= 0x40 && value <= 0x7e {
			if value != 'u' {
				return nil, 0, false
			}
			end = len(prefix) + index
			break
		}
	}
	if end < 0 {
		for _, value := range buffer[len(prefix):] {
			if (value < '0' || value > '9') && value != ';' && value != ':' {
				return nil, 0, false
			}
		}
		return nil, 0, true
	}
	payload := string(buffer[len(prefix):end])
	if payload == "" || strings.ContainsAny(payload, "~ABCDEFHPQRS<=>?") {
		return nil, 0, false
	}

	fields := strings.Split(payload, ";")
	keyCodes := strings.Split(fields[0], ":")
	code, err := strconv.Atoi(keyCodes[0])
	if err != nil {
		return nil, 0, false
	}
	modifiers := 1
	eventType := 1
	if len(fields) > 1 {
		modifierFields := strings.Split(fields[1], ":")
		if modifierFields[0] != "" {
			modifiers, err = strconv.Atoi(modifierFields[0])
			if err != nil {
				return nil, 0, false
			}
		}
		if len(modifierFields) > 1 {
			eventType, err = strconv.Atoi(modifierFields[1])
			if err != nil {
				return nil, 0, false
			}
		}
	}
	consumed := end + 1
	if eventType == 3 {
		return nil, consumed, false
	}

	const (
		shiftModifier = 1
		altModifier   = 2
		ctrlModifier  = 4
	)
	modifierBits := modifiers - 1
	if code == 13 {
		if modifierBits&shiftModifier != 0 {
			return &key{kind: keyNewline}, consumed, false
		}
		return &key{kind: keyEnter}, consumed, false
	}
	if len(fields) > 2 && fields[2] != "" {
		var text strings.Builder
		for _, value := range strings.Split(fields[2], ":") {
			point, parseErr := strconv.Atoi(value)
			if parseErr != nil || !utf8.ValidRune(rune(point)) {
				return nil, 0, false
			}
			text.WriteRune(rune(point))
		}
		return &key{kind: keyText, text: text.String()}, consumed, false
	}
	if modifierBits&altModifier != 0 && code == 'e' {
		return &key{kind: keyExternalEditor}, consumed, false
	}
	if modifierBits&ctrlModifier != 0 {
		switch code {
		case 'c':
			return &key{kind: keyCtrlC}, consumed, false
		case 'n':
			return &key{kind: keyNewline}, consumed, false
		case 'w':
			return &key{kind: keyCtrlW}, consumed, false
		case 'u':
			return &key{kind: keyPageUp}, consumed, false
		case 'd':
			return &key{kind: keyPageDown}, consumed, false
		case 'a':
			return &key{kind: keyHome}, consumed, false
		case 'e':
			return &key{kind: keyEnd}, consumed, false
		case 57350:
			return &key{kind: keyWordLeft}, consumed, false
		case 57351:
			return &key{kind: keyWordRight}, consumed, false
		}
	}
	switch code {
	case 9:
		return &key{kind: keyTab}, consumed, false
	case 27:
		return &key{kind: keyEscape}, consumed, false
	case 127:
		return &key{kind: keyBackspace}, consumed, false
	case 57349:
		return &key{kind: keyDelete}, consumed, false
	case 57350:
		return &key{kind: keyLeft}, consumed, false
	case 57351:
		return &key{kind: keyRight}, consumed, false
	case 57352:
		return &key{kind: keyUp}, consumed, false
	case 57353:
		return &key{kind: keyDown}, consumed, false
	case 57354:
		return &key{kind: keyPageUp}, consumed, false
	case 57355:
		return &key{kind: keyPageDown}, consumed, false
	case 57356:
		return &key{kind: keyHome}, consumed, false
	case 57357:
		return &key{kind: keyEnd}, consumed, false
	case 57365:
		return &key{kind: keyToggleMarkdown}, consumed, false
	case 57367:
		return &key{kind: keyExternalEditor}, consumed, false
	}
	return nil, consumed, false
}

func parseSGRMouse(buffer []byte) (*key, int, bool) {
	prefix := []byte("\x1b[<")
	if len(buffer) < len(prefix) {
		return nil, 0, bytes.HasPrefix(prefix, buffer)
	}
	if !bytes.HasPrefix(buffer, prefix) {
		return nil, 0, false
	}

	end := -1
	for index := len(prefix); index < len(buffer); index++ {
		if buffer[index] == 'M' || buffer[index] == 'm' {
			end = index
			break
		}
	}
	if end < 0 {
		return nil, 0, true
	}
	consumed := end + 1
	if buffer[end] != 'M' {
		return nil, consumed, false
	}

	fields := bytes.Split(buffer[len(prefix):end], []byte(";"))
	if len(fields) != 3 {
		return nil, consumed, false
	}
	button := 0
	if len(fields[0]) == 0 {
		return nil, consumed, false
	}
	for _, digit := range fields[0] {
		if digit < '0' || digit > '9' {
			return nil, consumed, false
		}
		button = button*10 + int(digit-'0')
	}
	if button&64 == 0 {
		return nil, consumed, false
	}
	switch button & 3 {
	case 0:
		return &key{kind: keyScrollUp}, consumed, false
	case 1:
		return &key{kind: keyScrollDown}, consumed, false
	default:
		return nil, consumed, false
	}
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
