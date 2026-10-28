package model

import (
	"strconv"
	"strings"
)

// AddArgs is the argument vector after /usr/sbin/ufw that adds r. A
// Darkpanel rule ends with "comment" and its tag as one argument; a manual
// rule ends with its own comment, if any, unless that comment starts with
// "-" or does not match the comment pattern, when it is left off (the rule
// is then written without its comment). r must be valid (Validate) except
// for a comment read from UFW's files, and, for a Darkpanel rule, carry its
// id.
func AddArgs(r Rule) []string {
	return withComment(r, actionPrefix(r, nil))
}

// DeleteArgs is the argument vector that deletes r: "delete" and the rule
// specification without the comment, which ufw matches whatever comment
// the stored rule carries.
func DeleteArgs(r Rule) []string {
	return actionPrefix(r, []string{"delete"})
}

// InsertArgs is the argument vector that adds r at position pos (1-based,
// as ufw numbers its rules), for the inverse of a delete; the comment
// follows the rule of AddArgs.
func InsertArgs(pos int, r Rule) []string {
	return withComment(r, actionPrefix(r, []string{"insert", strconv.Itoa(pos)}))
}

// actionPrefix renders "[route] <verb...> <action> <rule spec>".
func actionPrefix(r Rule, verb []string) []string {
	var args []string
	if r.Direction == DirRoute {
		args = append(args, "route")
	}
	args = append(args, verb...)
	args = append(args, string(r.Action))
	return append(args, spec(r)...)
}

// spec renders the rule specification in the order ufw's grammar requires:
// direction and interfaces, then the log type, then protocol, source and
// destination ("ufw allow log in" is refused as an invalid interface
// clause).
func spec(r Rule) []string {
	var args []string
	switch r.Direction {
	case DirRoute:
		if r.InIface != "" {
			args = append(args, "in", "on", r.InIface)
		}
		if r.OutIface != "" {
			args = append(args, "out", "on", r.OutIface)
		}
	case DirIn:
		args = append(args, "in")
		if r.InIface != "" {
			args = append(args, "on", r.InIface)
		}
	case DirOut:
		args = append(args, "out")
		if r.OutIface != "" {
			args = append(args, "on", r.OutIface)
		}
	}
	if r.Log == LogOn || r.Log == LogAll {
		args = append(args, string(r.Log))
	}
	if r.Proto != ProtoAny {
		args = append(args, "proto", string(r.Proto))
	}
	args = append(args, "from", argAddr(r.From, r.Family))
	if r.FromPort != "" {
		args = append(args, "port", r.FromPort)
	}
	args = append(args, "to", argAddr(r.To, r.Family))
	if r.ToPort != "" {
		args = append(args, "port", r.ToPort)
	}
	return args
}

// argAddr writes any as the family's zero prefix for a single-family rule:
// ufw writes a rule whose addresses are both "any" into both files.
func argAddr(addr string, fam Family) string {
	if addr != Any {
		return addr
	}
	switch fam {
	case FamilyV4:
		return "0.0.0.0/0"
	case FamilyV6:
		return "::/0"
	}
	return Any
}

// withComment appends the comment argument. UFW's files can hold a comment
// the schema patterns of spec 2.2 would refuse, and a manual rule's comment
// is its own argument, which must not start with "-" since ufw has no "--".
// Such a comment is left off, so a rule read from UFW's files can be written
// again, without that comment; a Darkpanel rule keeps its tag.
func withComment(r Rule, args []string) []string {
	if r.Origin == OriginManual {
		if r.Comment == "" || !commentRe.MatchString(r.Comment) || strings.HasPrefix(r.Comment, "-") {
			return args
		}
		return append(args, "comment", r.Comment)
	}
	label := r.Comment
	if !commentRe.MatchString(label) {
		label = ""
	}
	return append(args, "comment", Tag(r.ID, label))
}
