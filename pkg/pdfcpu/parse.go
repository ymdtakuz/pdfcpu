/*
Copyright 2018 The pdfcpu Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pdfcpu

import (
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"

	"github.com/pdfcpu/pdfcpu/pkg/log"
	"github.com/pkg/errors"
)

var (
	errArrayCorrupt            = errors.New("pdfcpu: parse: corrupt array")
	errArrayNotTerminated      = errors.New("pdfcpu: parse: unterminated array")
	errDictionaryCorrupt       = errors.New("pdfcpu: parse: corrupt dictionary")
	errDictionaryDuplicateKey  = errors.New("pdfcpu: parse: duplicate key")
	errDictionaryNotTerminated = errors.New("pdfcpu: parse: unterminated dictionary")
	errHexLiteralCorrupt       = errors.New("pdfcpu: parse: corrupt hex literal")
	errHexLiteralNotTerminated = errors.New("pdfcpu: parse: hex literal not terminated")
	errNameObjectCorrupt       = errors.New("pdfcpu: parse: corrupt name object")
	errNoArray                 = errors.New("pdfcpu: parse: no array")
	errNoDictionary            = errors.New("pdfcpu: parse: no dictionary")
	errStringLiteralCorrupt    = errors.New("pdfcpu: parse: corrupt string literal, possibly unbalanced parenthesis")
	errBufNotAvailable         = errors.New("pdfcpu: parse: no buffer available")
	errXrefStreamMissingW      = errors.New("pdfcpu: parse: xref stream dict missing entry W")
	errXrefStreamCorruptW      = errors.New("pdfcpu: parse: xref stream dict corrupt entry W: expecting array of 3 int")
	errXrefStreamCorruptIndex  = errors.New("pdfcpu: parse: xref stream dict corrupt entry Index")
	errObjStreamMissingN       = errors.New("pdfcpu: parse: obj stream dict missing entry W")
	errObjStreamMissingFirst   = errors.New("pdfcpu: parse: obj stream dict missing entry First")
)

func positionToNextWhitespace(s string) (int, string) {

	for i, c := range s {
		if unicode.IsSpace(c) {
			return i, s[i:]
		}
	}
	return 0, s
}

// PositionToNextWhitespaceOrChar trims a string to next whitespace or one of given chars.
// Returns the index of the position or -1 if no match.
func positionToNextWhitespaceOrChar(s, chars string) (int, string) {

	if len(chars) == 0 {
		return positionToNextWhitespace(s)
	}

	for i, c := range s {
		for _, m := range chars {
			if c == m || unicode.IsSpace(c) {
				return i, s[i:]
			}
		}
	}

	return -1, s
}

func positionToNextEOL(s string) string {

	chars := "\x0A\x0D"

	for i, c := range s {
		for _, m := range chars {
			if c == m {
				return s[i:]
			}
		}
	}
	return ""
}

// trimLeftSpace trims leading whitespace and trailing comment.
func trimLeftSpace(s string) (outstr string, trimmedSpaces int) {

	log.Parse.Printf("TrimLeftSpace: begin %s\n", s)

	whitespace := func(c rune) bool { return unicode.IsSpace(c) }

	outstr = s

	for {
		// trim leading whitespace
		outstr = strings.TrimLeftFunc(outstr, whitespace)
		log.Parse.Printf("1 outstr: <%s>\n", outstr)
		if len(outstr) <= 1 || outstr[0] != '%' {
			break
		}
		// trim PDF comment (= '%' up to eol)
		outstr = positionToNextEOL(outstr)
		log.Parse.Printf("2 outstr: <%s>\n", outstr)

	}

	trimmedSpaces = len(s) - len(outstr)

	log.Parse.Printf("TrimLeftSpace: end %s %d\n", outstr, trimmedSpaces)

	return outstr, trimmedSpaces
}

// HexString validates and formats a hex string to be of even length.
func hexString(s string) (*string, bool) {

	log.Parse.Printf("HexString(%s)\n", s)

	if len(s) == 0 {
		s1 := ""
		return &s1, true
	}

	uc := strings.ToUpper(s)
	var out []byte

	for _, c := range []byte(uc) {
		log.Parse.Printf("checking <%c>\n", c)
		switch {
		case c >= 0x30 && c <= 0x39: // 0 - 9
			out = append(out, c)
		case c >= 0x41 && c <= 0x46: // A - F
			out = append(out, c)
		case c == 0x0A || c == 0x0D: // LF or CR
			continue
		default:
			log.Parse.Println("isHexStr returning false")
			return nil, false
		}
	}

	log.Parse.Println("isHexStr returning true")

	// If the final digit of a hexadecimal string is missing -
	// that is, if there is an odd number of digits - the final digit shall be assumed to be 0.
	if len(out)%2 == 1 {
		out = append(out, 0x30)
	}
	so := string(out)

	return &so, true
}

// balancedParenthesesPrefix returns the index of the end position of the balanced parentheses prefix of s
// or -1 if unbalanced. s has to start with '('
func balancedParenthesesPrefix(s string) int {

	var j int
	escaped := false

	for i := 0; i < len(s); i++ {

		c := s[i]

		if !escaped && c == '\\' {
			escaped = true
			continue
		}

		if escaped {
			escaped = false
			continue
		}

		if c == '(' {
			j++
		}

		if c == ')' {
			j--
		}

		if j == 0 {
			return i
		}

	}

	return -1
}

func forwardParseBuf(buf string, pos int) string {
	if pos < len(buf) {
		return buf[pos:]
	}

	return ""
}

func delimiter(b byte) bool {

	s := "<>[]()/"

	for i := 0; i < len(s); i++ {
		if b == s[i] {
			return true
		}
	}

	return false
}

// parseObjectAttributes parses object number and generation of the next object for given string buffer.
func parseObjectAttributes(line *string) (objectNumber *int, generationNumber *int, err error) {

	log.Parse.Printf("ParseObjectAttributes: buf=<%s>\n", *line)

	if line == nil || len(*line) == 0 {
		return nil, nil, errors.New("pdfcpu: ParseObjectAttributes: buf not available")
	}

	l := *line
	var remainder string

	i := strings.Index(l, "obj")
	if i < 0 {
		return nil, nil, errors.New("pdfcpu: ParseObjectAttributes: can't find \"obj\"")
	}

	remainder = l[i+len("obj"):]
	l = l[:i]

	// object number

	l, _ = trimLeftSpace(l)
	if len(l) == 0 {
		return nil, nil, errors.New("pdfcpu: ParseObjectAttributes: can't find object number")
	}

	i, _ = positionToNextWhitespaceOrChar(l, "%")
	if i <= 0 {
		return nil, nil, errors.New("pdfcpu: ParseObjectAttributes: can't find end of object number")
	}

	objNr, err := strconv.Atoi(l[:i])
	if err != nil {
		return nil, nil, err
	}

	// generation number

	l = l[i:]
	l, _ = trimLeftSpace(l)
	if len(l) == 0 {
		return nil, nil, errors.New("pdfcpu: ParseObjectAttributes: can't find generation number")
	}

	i, _ = positionToNextWhitespaceOrChar(l, "%")
	if i <= 0 {
		return nil, nil, errors.New("pdfcpu: ParseObjectAttributes: can't find end of generation number")
	}

	genNr, err := strconv.Atoi(l[:i])
	if err != nil {
		return nil, nil, err
	}

	objectNumber = &objNr
	generationNumber = &genNr

	*line = remainder

	return objectNumber, generationNumber, nil
}

func parseArray(line *string) (*Array, error) {

	if line == nil || len(*line) == 0 {
		return nil, errNoArray
	}

	l := *line

	log.Parse.Printf("ParseArray: %s\n", l)

	if !strings.HasPrefix(l, "[") {
		return nil, errArrayCorrupt
	}

	if len(l) == 1 {
		return nil, errArrayNotTerminated
	}

	// position behind '['
	l = forwardParseBuf(l, 1)

	// position to first non whitespace char after '['
	l, _ = trimLeftSpace(l)

	if len(l) == 0 {
		// only whitespace after '['
		return nil, errArrayNotTerminated
	}

	a := Array{}

	for !strings.HasPrefix(l, "]") {

		obj, err := parseObject(&l)
		if err != nil {
			return nil, err
		}
		if obj != nil {
			log.Parse.Printf("ParseArray: new array obj=%v\n", obj)
			a = append(a, obj)
		}
		// we are positioned on the char behind the last parsed array entry.
		if len(l) == 0 {
			return nil, errArrayNotTerminated
		}

		// position to next non whitespace char.
		l, _ = trimLeftSpace(l)
		if len(l) == 0 {
			return nil, errArrayNotTerminated
		}
	}

	// position behind ']'
	l = forwardParseBuf(l, 1)

	*line = l

	log.Parse.Printf("ParseArray: returning array (len=%d): %v\n", len(a), a)

	return &a, nil
}

func parseStringLiteral(line *string) (Object, error) {

	// Balanced pairs of parenthesis are allowed.
	// Empty literals are allowed.
	// \ needs special treatment.
	// Allowed escape sequences:
	// \n	x0A
	// \r	x0D
	// \t	x09
	// \b	x08
	// \f	xFF
	// \(	x28
	// \)	x29
	// \\	x5C
	// \ddd octal code sequence, d=0..7

	// Ignore '\' for undefined escape sequences.

	// Unescaped 0x0A,0x0D or combination gets parsed as 0x0A.

	// Join split lines by '\' eol.

	if line == nil || len(*line) == 0 {
		return nil, errBufNotAvailable
	}

	l := *line

	log.Parse.Printf("parseStringLiteral: begin <%s>\n", l)

	if len(l) < 2 || !strings.HasPrefix(l, "(") {
		return nil, errStringLiteralCorrupt
	}

	// Calculate prefix with balanced parentheses,
	// return index of enclosing ')'.
	i := balancedParenthesesPrefix(l)
	if i < 0 {
		// No balanced parentheses.
		return nil, errStringLiteralCorrupt
	}

	// remove enclosing '(', ')'
	balParStr := l[1:i]

	// Parse string literal, see 7.3.4.2
	//str := stringLiteral(balParStr)

	// position behind ')'
	*line = forwardParseBuf(l[i:], 1)

	stringLiteral := StringLiteral(balParStr)
	log.Parse.Printf("parseStringLiteral: end <%s>\n", stringLiteral)

	return stringLiteral, nil
}

func parseHexLiteral(line *string) (Object, error) {

	// hexliterals have no whitespace and can't be empty.

	if line == nil || len(*line) == 0 {
		return nil, errBufNotAvailable
	}

	l := *line

	log.Parse.Printf("parseHexLiteral: %s\n", l)

	if len(l) < 3 || !strings.HasPrefix(l, "<") {
		return nil, errHexLiteralCorrupt
	}

	// position behind '<'
	l = forwardParseBuf(l, 1)

	eov := strings.Index(l, ">") // end of hex literal.
	if eov < 0 {
		return nil, errHexLiteralNotTerminated
	}

	hexStr, ok := hexString(l[:eov])
	if !ok {
		return nil, errHexLiteralCorrupt
	}

	// position behind '>'
	*line = forwardParseBuf(l[eov:], 1)

	return HexLiteral(*hexStr), nil
}

func validateNameHexSequence(s string) error {

	for i := 0; i < len(s); {
		c := s[i]
		if c != '#' {
			i++
			continue
		}

		// # detected, next 2 chars have to exist.
		if len(s) < i+3 {
			return errNameObjectCorrupt
		}

		s1 := s[i+1 : i+3]

		// And they have to be hex characters.
		_, err := hex.DecodeString(s1)
		if err != nil {
			return errNameObjectCorrupt
		}

		i += 3
	}

	return nil
}

func parseName(line *string) (*Name, error) {

	// see 7.3.5

	if line == nil || len(*line) == 0 {
		return nil, errBufNotAvailable
	}

	l := *line

	log.Parse.Printf("parseNameObject: %s\n", l)

	if len(l) < 2 || !strings.HasPrefix(l, "/") {
		return nil, errNameObjectCorrupt
	}

	// position behind '/'
	l = forwardParseBuf(l, 1)

	// cut off on whitespace or delimiter
	eok, _ := positionToNextWhitespaceOrChar(l, "/<>()[]")
	if eok < 0 {
		// Name terminated by eol.
		*line = ""
	} else {
		*line = l[eok:]
		l = l[:eok]
	}

	// Validate optional #xx sequences
	err := validateNameHexSequence(l)
	if err != nil {
		return nil, err
	}

	nameObj := Name(l)
	return &nameObj, nil
}

func parseDict(line *string) (*Dict, error) {

	if line == nil || len(*line) == 0 {
		return nil, errNoDictionary
	}

	l := *line

	log.Parse.Printf("ParseDict: %s\n", l)

	if len(l) < 4 || !strings.HasPrefix(l, "<<") {
		return nil, errDictionaryCorrupt
	}

	// position behind '<<'
	l = forwardParseBuf(l, 2)

	// position to first non whitespace char after '<<'
	l, _ = trimLeftSpace(l)

	if len(l) == 0 {
		// only whitespace after '['
		return nil, errDictionaryNotTerminated
	}

	d := NewDict()

	for !strings.HasPrefix(l, ">>") {

		key, err := parseName(&l)
		if err != nil {
			return nil, err
		}
		log.Parse.Printf("ParseDict: key = %s\n", key)

		// position to first non whitespace after key
		l, _ = trimLeftSpace(l)

		if len(l) == 0 {
			log.Parse.Println("ParseDict: only whitespace after key")
			// only whitespace after key
			return nil, errDictionaryNotTerminated
		}

		obj, err := parseObject(&l)
		if err != nil {
			return nil, err
		}

		// Specifying the null object as the value of a dictionary entry (7.3.7, "Dictionary Objects")
		// shall be equivalent to omitting the entry entirely.
		if obj != nil {
			log.Parse.Printf("ParseDict: dict[%s]=%v\n", key, obj)
			if ok := d.Insert(string(*key), obj); !ok {
				return nil, errDictionaryDuplicateKey
			}
		}

		// we are positioned on the char behind the last parsed dict value.
		if len(l) == 0 {
			return nil, errDictionaryNotTerminated
		}

		// position to next non whitespace char.
		l, _ = trimLeftSpace(l)
		if len(l) == 0 {
			return nil, errDictionaryNotTerminated
		}

	}

	// position behind '>>'
	l = forwardParseBuf(l, 2)

	*line = l

	log.Parse.Printf("ParseDict: returning dict at: %v\n", d)

	return &d, nil
}

func noBuf(l *string) bool {
	return l == nil || len(*l) == 0
}

func parseNumericOrIndRef(line *string) (Object, error) {

	if noBuf(line) {
		return nil, errBufNotAvailable
	}

	l := *line

	// if this object is an integer we need to check for an indirect reference eg. 1 0 R
	// otherwise it has to be a float
	// we have to check first for integer

	i1, _ := positionToNextWhitespaceOrChar(l, "/<([]>")
	var l1 string
	if i1 > 0 {
		l1 = l[i1:]
	} else {
		l1 = l[len(l):]
	}

	str := l
	if i1 > 0 {
		str = l[:i1]
	}

	var ip *int
	var fp *float64
	// Try int
	if i, err := strconv.Atoi(str); err != nil {
		// Try float
		f, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return nil, err
		}
		fp = &f
	} else {
		ip = &i
	}

	// if not followed by whitespace return sole integer value.
	if i1 <= 0 || delimiter(l[i1]) {
		*line = l1
		if fp != nil {
			log.Parse.Printf("parseNumericOrIndRef: value is numeric float: %d\n", *fp)
			return Float(*fp), nil
		}
		log.Parse.Printf("parseNumericOrIndRef: value is numeric int: %d\n", *ip)
		return Integer(*ip), nil
	}

	// Must be indirect reference. (123 0 R)
	// Missing is the 2nd int and "R".

	iref1 := -1
	if ip != nil {
		iref1 = *ip
	}

	l = l[i1:]
	l, _ = trimLeftSpace(l)
	if len(l) == 0 {
		// only whitespace
		*line = l1
		if fp != nil {
			return Float(*fp), nil
		}
		return Integer(*ip), nil
	}

	i2, _ := positionToNextWhitespaceOrChar(l, "/<([]>")

	// if only 2 token, can't be indirect reference.
	// if not followed by whitespace return sole integer value.
	if i2 <= 0 || delimiter(l[i2]) {
		*line = l1
		if fp != nil {
			log.Parse.Printf("parseNumericOrIndRef: 2 objects => value is numeric float: %d\n", *fp)
			return Float(*fp), nil
		}
		log.Parse.Printf("parseNumericOrIndRef: 2 objects => value is numeric int: %d\n", *ip)
		return Integer(*ip), nil
	}

	str = l
	if i2 > 0 {
		str = l[:i2]
	}

	iref2, err := strconv.Atoi(str)
	if err != nil {
		// 2nd int(generation number) not available.
		// Can't be an indirect reference.
		*line = l1
		if fp != nil {
			log.Parse.Printf("parseNumericOrIndRef: 3 objects, 2nd no int, value is no indirect ref but numeric float: %d\n", *fp)
			return Float(*fp), nil
		}
		log.Parse.Printf("parseNumericOrIndRef: 3 objects, 2nd no int, value is no indirect ref but numeric int: %d\n", *ip)
		return Integer(*ip), nil
	}

	// We have the 2nd int(generation number).
	// Look for "R"

	l = l[i2:]
	l, _ = trimLeftSpace(l)

	if len(l) == 0 {
		// only whitespace
		l = l1
		if fp != nil {
			return Float(*fp), nil
		}
		return Integer(*ip), nil
	}

	if l[0] == 'R' {
		// We have all 3 components to create an indirect reference.
		*line = forwardParseBuf(l, 1)
		if iref1 < 0 {
			return nil, nil
		}
		return *NewIndirectRef(iref1, iref2), nil
	}

	// 'R' not available.
	// Can't be an indirect reference.
	*line = l1

	if fp != nil {
		log.Parse.Printf("parseNumericOrIndRef: value is no indirect ref(no 'R') but numeric float: %d\n", *fp)
		return Float(*fp), nil
	}
	log.Parse.Printf("parseNumericOrIndRef: value is no indirect ref(no 'R') but numeric int: %d\n", *ip)
	return Integer(*ip), nil
}

func parseHexLiteralOrDict(l *string) (val Object, err error) {

	if len(*l) < 2 {
		return nil, errBufNotAvailable
	}

	// if next char = '<' parseDict.
	if (*l)[1] == '<' {
		log.Parse.Println("parseHexLiteralOrDict: value = Dictionary")
		d, err := parseDict(l)
		if err != nil {
			return nil, err
		}
		val = *d
	} else {
		// hex literals
		log.Parse.Println("parseHexLiteralOrDict: value = Hex Literal")
		if val, err = parseHexLiteral(l); err != nil {
			return nil, err
		}
	}

	return val, nil
}

func parseBooleanOrNull(l string) (val Object, s string, ok bool) {

	// null, absent object
	if strings.HasPrefix(l, "null") {
		log.Parse.Println("parseBoolean: value = null")
		return nil, "null", true
	}

	// boolean true
	if strings.HasPrefix(l, "true") {
		log.Parse.Println("parseBoolean: value = true")
		return Boolean(true), "true", true
	}

	// boolean false
	if strings.HasPrefix(l, "false") {
		log.Parse.Println("parseBoolean: value = false")
		return Boolean(false), "false", true
	}

	return nil, "", false
}

// parseObject parses next Object from string buffer and returns the updated (left clipped) buffer.
func parseObject(line *string) (Object, error) {

	if noBuf(line) {
		// only whitespace returns empty StringLiteral
		return StringLiteral(""), nil
	}

	l := *line

	log.Parse.Printf("ParseObject: buf= <%s>\n", l)

	// position to first non whitespace char
	l, _ = trimLeftSpace(l)
	if len(l) == 0 {
		// only whitespace returns empty StringLiteral
		return StringLiteral(""), nil
	}

	var value Object
	var err error

	switch l[0] {

	case '[': // array
		log.Parse.Println("ParseObject: value = Array")
		a, err := parseArray(&l)
		if err != nil {
			return nil, err
		}
		value = *a

	case '/': // name
		log.Parse.Println("ParseObject: value = Name Object")
		nameObj, err := parseName(&l)
		if err != nil {
			return nil, err
		}
		value = *nameObj

	case '<': // hex literal or dict
		value, err = parseHexLiteralOrDict(&l)
		if err != nil {
			return nil, err
		}

	case '(': // string literal
		log.Parse.Printf("ParseObject: value = String Literal: <%s>\n", l)
		if value, err = parseStringLiteral(&l); err != nil {
			return nil, err
		}

	default:
		var valStr string
		var ok bool
		value, valStr, ok = parseBooleanOrNull(l)
		if ok {
			l = forwardParseBuf(l, len(valStr))
			break
		}
		// Must be numeric or indirect reference:
		// int 0 r
		// int
		// float
		if value, err = parseNumericOrIndRef(&l); err != nil {
			return nil, err
		}

	}

	log.Parse.Printf("ParseObject returning %v\n", value)

	*line = l

	return value, nil
}

// parseXRefStreamDict creates a XRefStreamDict out of a StreamDict.
func parseXRefStreamDict(sd *StreamDict) (*XRefStreamDict, error) {

	log.Parse.Println("ParseXRefStreamDict: begin")

	if sd.Size() == nil {
		return nil, errors.New("pdfcpu: ParseXRefStreamDict: \"Size\" not available")
	}

	objs := []int{}

	//	Read optional parameter Index
	indArr := sd.Index()
	if indArr != nil {
		log.Parse.Println("ParseXRefStreamDict: using index dict")

		//indArr := *pIndArr
		if len(indArr)%2 > 1 {
			return nil, errXrefStreamCorruptIndex
		}

		for i := 0; i < len(indArr)/2; i++ {

			startObj, ok := indArr[i*2].(Integer)
			if !ok {
				return nil, errXrefStreamCorruptIndex
			}

			count, ok := indArr[i*2+1].(Integer)
			if !ok {
				return nil, errXrefStreamCorruptIndex
			}

			for j := 0; j < count.Value(); j++ {
				objs = append(objs, startObj.Value()+j)
			}
		}

	} else {
		log.Parse.Println("ParseXRefStreamDict: no index dict")
		for i := 0; i < *sd.Size(); i++ {
			objs = append(objs, i)

		}
	}

	// Read parameter W in order to decode the xref table.
	// array of integers representing the size of the fields in a single cross-reference entry.

	var wIntArr [3]int

	a := sd.W()
	if a == nil {
		return nil, errXrefStreamMissingW
	}

	//arr := *w
	// validate array with 3 positive integers
	if len(a) != 3 {
		return nil, errXrefStreamCorruptW
	}

	f := func(ok bool, i int) bool {
		return !ok || i < 0
	}

	i1, ok := a[0].(Integer)
	if f(ok, i1.Value()) {
		return nil, errXrefStreamCorruptW
	}
	wIntArr[0] = int(i1)

	i2, ok := a[1].(Integer)
	if f(ok, i2.Value()) {
		return nil, errXrefStreamCorruptW
	}
	wIntArr[1] = int(i2)

	i3, ok := a[2].(Integer)
	if f(ok, i3.Value()) {
		return nil, errXrefStreamCorruptW
	}
	wIntArr[2] = int(i3)

	xsd := XRefStreamDict{
		StreamDict:     *sd,
		Size:           *sd.Size(),
		Objects:        objs,
		W:              wIntArr,
		PreviousOffset: sd.Prev(),
	}

	log.Parse.Println("ParseXRefStreamDict: end")

	return &xsd, nil
}

// objectStreamDict creates a ObjectStreamDict out of a StreamDict.
func objectStreamDict(sd *StreamDict) (*ObjectStreamDict, error) {

	if sd.First() == nil {
		return nil, errObjStreamMissingFirst
	}

	if sd.N() == nil {
		return nil, errObjStreamMissingN
	}

	osd := ObjectStreamDict{
		StreamDict:     *sd,
		ObjCount:       *sd.N(),
		FirstObjOffset: *sd.First(),
		ObjArray:       nil}

	return &osd, nil
}
