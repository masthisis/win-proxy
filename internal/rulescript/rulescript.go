// Package rulescript compiles tiny user JavaScript snippets into mihomo
// rule lines (issue #17, scriptable rules).
//
// THREAT MODEL (the reason this package exists in this shape): a snippet
// never touches the data plane. It runs inside goja — a pure-Go ES6
// interpreter — with ZERO host bindings: no filesystem, no network, no
// process, no timers (nothing is registered beyond the rule-constructor
// helpers). Evaluation happens in mihomo, not here; a script can only
// PRODUCE rule text that the core then matches traffic against. The result
// must be deterministic (checked by a double run) and bounded (size caps).
//
// ENGINE CHOICE (issue #17 asked for one, justified): goja, not expr-lang.
// expr is ~3 MB smaller, but every competing client embeds JavaScript for
// exactly this feature — Clash Verge Rev (boa), Surge, Loon, Quantumult X —
// so the community's script corpus is JS and copy-paste compatibility
// matters more than binary size on a desktop app. Sandboxing is equivalent:
// both engines expose nothing unless the host registers it.
//
// OUTPUT CONTRACT: a script returns
//   - a string,            -> one rule
//   - an array of strings, -> many rules
//   - undefined/null/""    -> no rules (the script is a no-op)
//
// The source is evaluated inside a strict-mode IIFE wrapper, so scripts use
// plain top-level `return` statements (the same ergonomics Clash Verge Rev
// scripts offer); reported syntax-error line numbers are adjusted back to
// the user's source.
//
// Rules come from helpers (domainSuffix("x.com", "PROXY")) or raw strings.
// Helpers without a policy produce the INNER form ("DOMAIN-SUFFIX,x.com")
// which nests into and()/or()/not(). Every emitted line is validated here
// (shape), and again at generation time (policy must exist in the config);
// mihomo is the final semantic validator — the documented two-layer
// discipline.
package rulescript

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// Limits — all enforced, all reported with a precise error.
const (
	// MaxSourceBytes caps the snippet itself (settings sanitizes to the same).
	MaxSourceBytes = 32 << 10
	// MaxOutputRules caps how many rules one script may emit.
	MaxOutputRules = 256
	// MaxRuleLen caps one emitted rule line.
	MaxRuleLen = 1024
	// MaxTotalBytes caps the combined emitted output.
	MaxTotalBytes = 128 << 10
	// RunBudget is the wall-clock budget for ONE evaluation.
	RunBudget = 2 * time.Second
	// MaxCallStack caps recursion depth (goja SetMaxCallStackSize).
	MaxCallStack = 128
)

// ruleTypes is the verified mihomo rule-type surface (wiki.metacubex.one/en/
// config/rules/, checked against v1.19.34). REMATCH-NAME is intentionally
// ABSENT from the helper surface — it only applies to listener traffic —
// but the validator accepts it so hand-written lines still pass.
var ruleTypes = map[string]bool{
	"DOMAIN": true, "DOMAIN-SUFFIX": true, "DOMAIN-KEYWORD": true,
	"DOMAIN-WILDCARD": true, "DOMAIN-REGEX": true,
	"GEOSITE": true,
	"IP-CIDR": true, "IP-CIDR6": true, "IP-SUFFIX": true,
	"SRC-IP-CIDR": true, "SRC-IP-SUFFIX": true,
	"IP-ASN": true, "SRC-IP-ASN": true,
	"GEOIP": true, "SRC-GEOIP": true,
	"SRC-PORT": true, "DST-PORT": true, "IN-PORT": true,
	"IN-TYPE": true, "IN-USER": true, "IN-NAME": true,
	"PROCESS-PATH": true, "PROCESS-PATH-WILDCARD": true, "PROCESS-PATH-REGEX": true,
	"PROCESS-NAME": true, "PROCESS-NAME-WILDCARD": true, "PROCESS-NAME-REGEX": true,
	"UID": true, "NETWORK": true, "DSCP": true,
	"RULE-SET": true, "REMATCH-NAME": true,
	"AND": true, "OR": true, "NOT": true, "SUB-RULE": true,
	"MATCH": true,
}

// logicOps are the combinators that take a parenthesized rule group.
var logicOps = map[string]bool{"AND": true, "OR": true, "NOT": true}

// Compile evaluates the script and returns its rule lines.
//
// It runs the script TWICE in fresh runtimes and requires identical output:
// rule generation feeds a config file that mihomo reloads at arbitrary
// times, so time/Math.random-dependent output would silently reorder
// traffic between reloads — such scripts are rejected with a precise
// error instead.
func Compile(source string) ([]string, error) {
	if strings.TrimSpace(source) == "" {
		return nil, nil // an empty script is a no-op, not an error
	}
	if err := checkSource(source); err != nil {
		return nil, err
	}
	first, err := runOnce(iife(source))
	if err != nil {
		return nil, err
	}
	second, err := runOnce(iife(source))
	if err != nil {
		return nil, err
	}
	if strings.Join(first, "\n") != strings.Join(second, "\n") || len(first) != len(second) {
		return nil, errors.New("script output is not deterministic " +
			"(Math.random / Date.now?); rule generation must be stable across runs")
	}
	return first, nil
}

// iife wraps the user source in a strict-mode function IIFE so a plain
// top-level `return` works; the newline keeps reported line numbers at the
// user's (compileErr shifts them back).
func iife(source string) string {
	return "(function(){\n" + source + "\n})()"
}

// checkSource enforces the source caps before anything runs.
func checkSource(source string) error {
	if len(source) > MaxSourceBytes {
		return fmt.Errorf("script too large (%d bytes, max %d)", len(source), MaxSourceBytes)
	}
	return nil
}

// runOnce compiles + evaluates one script source in a fresh sandbox.
func runOnce(source string) ([]string, error) {
	prog, err := goja.Compile("script", source, true)
	if err != nil {
		return nil, compileErr(err)
	}

	vm := goja.New()
	vm.SetMaxCallStackSize(MaxCallStack)
	registerHelpers(vm)

	// Hard wall-clock budget: the documented goja interrupt pattern — the
	// timer fires cross-goroutine and RunProgram returns immediately.
	timer := time.AfterFunc(RunBudget, func() { vm.Interrupt("run budget exceeded") })
	defer timer.Stop()

	v, err := vm.RunProgram(prog)
	if err != nil {
		var ie *goja.InterruptedError
		if errors.As(err, &ie) {
			return nil, fmt.Errorf("script did not finish within %s (infinite loop?)", RunBudget)
		}
		return nil, fmt.Errorf("runtime error: %w", err)
	}

	rules, err := extractRules(v)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if err := ValidateRuleLine(r); err != nil {
			return nil, fmt.Errorf("invalid rule %q: %w", r, err)
		}
	}
	return rules, nil
}

// compileErr renders goja syntax errors with line-precise positions (the
// two-layer validation discipline: errors surface BEFORE anything applies).
func compileErr(err error) error {
	var ce *goja.CompilerSyntaxError
	if errors.As(err, &ce) {
		p := ce.File.Position(ce.Offset)
		// The IIFE wrapper consumed one leading line — map back to the user's
		// source coordinates.
		line := p.Line - 1
		if line < 1 {
			line = 1
		}
		return fmt.Errorf("syntax error at line %d, col %d: %s", line, p.Column, ce.Message)
	}
	return err
}

// extractRules converts the script's return value to rule lines.
func extractRules(v goja.Value) ([]string, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	if s, ok := v.Export().(string); ok {
		if s == "" {
			return nil, nil
		}
		return capOutput([]string{s})
	}
	arr, ok := v.Export().([]interface{})
	if !ok {
		return nil, fmt.Errorf("script must return a string or an array of strings (got %T)", v.Export())
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("array items must be strings (got %T)", item)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return capOutput(out)
}

// capOutput enforces the output caps.
func capOutput(rules []string) ([]string, error) {
	if len(rules) > MaxOutputRules {
		return nil, fmt.Errorf("script emitted %d rules (max %d)", len(rules), MaxOutputRules)
	}
	total := 0
	for _, r := range rules {
		if len(r) > MaxRuleLen {
			return nil, fmt.Errorf("rule too long (%d bytes, max %d): %.60s...", len(r), MaxRuleLen, r)
		}
		total += len(r) + 1
	}
	if total > MaxTotalBytes {
		return nil, fmt.Errorf("script output too large (%d bytes, max %d)", total, MaxTotalBytes)
	}
	return rules, nil
}

// ------------------------------------------------------------- sandbox ----

// registerHelpers installs the ONLY globals a script gets: rule
// constructors. Everything is pure string shaping with argument validation
// — no host capability exists behind any of them.
//
// Helpers without a policy return the INNER form ("DOMAIN-SUFFIX,x.com");
// with a policy they return a final rule. ipCidr auto-switches to IP-CIDR6
// for IPv6 literals (mihomo keeps the two types separate, same as the
// routing-override editor #104).
func registerHelpers(vm *goja.Runtime) {
	plain := map[string]string{
		"domain":              "DOMAIN",
		"domainSuffix":        "DOMAIN-SUFFIX",
		"domainKeyword":       "DOMAIN-KEYWORD",
		"domainWildcard":      "DOMAIN-WILDCARD",
		"domainRegex":         "DOMAIN-REGEX",
		"geosite":             "GEOSITE",
		"ipCidr":              "IP-CIDR",
		"ipCidr6":             "IP-CIDR6",
		"srcIpCidr":           "SRC-IP-CIDR",
		"ipSuffix":            "IP-SUFFIX",
		"srcIpSuffix":         "SRC-IP-SUFFIX",
		"geoip":               "GEOIP",
		"srcGeoip":            "SRC-GEOIP",
		"ipAsn":               "IP-ASN",
		"srcIpAsn":            "SRC-IP-ASN",
		"dstPort":             "DST-PORT",
		"srcPort":             "SRC-PORT",
		"inPort":              "IN-PORT",
		"inType":              "IN-TYPE",
		"inUser":              "IN-USER",
		"inName":              "IN-NAME",
		"network":             "NETWORK",
		"dscp":                "DSCP",
		"uid":                 "UID",
		"processName":         "PROCESS-NAME",
		"processNameWildcard": "PROCESS-NAME-WILDCARD",
		"processNameRegex":    "PROCESS-NAME-REGEX",
		"processPath":         "PROCESS-PATH",
		"processPathWildcard": "PROCESS-PATH-WILDCARD",
		"processPathRegex":    "PROCESS-PATH-REGEX",
		"ruleSet":             "RULE-SET",
	}
	for name, typ := range plain {
		vm.Set(name, ruleFunc(vm, typ))
	}
	vm.Set("rule", ruleFunc(vm, "")) // rule(inner, policy): inner form -> final
	vm.Set("and", logicFunc(vm, "AND"))
	vm.Set("or", logicFunc(vm, "OR"))
	vm.Set("not", logicFunc(vm, "NOT"))
	vm.Set("match", func(call goja.FunctionCall) goja.Value {
		policy, err := argString(call, 0, "match(policy)")
		if err != nil {
			panicJs(vm, err)
		}
		line := "MATCH," + policy
		if err := ValidateRuleLine(line); err != nil {
			panicJs(vm, err)
		}
		return vm.ToValue(line)
	})
}

// ruleFunc builds a rule-constructor helper. typ == "" is the generic
// rule(inner, policy) form.
func ruleFunc(vm *goja.Runtime, typ string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		payload, err := argString(call, 0, helperName(typ))
		if err != nil {
			panicJs(vm, err)
		}
		payload = strings.TrimSpace(payload)
		if payload == "" {
			panicJs(vm, errors.New("payload must not be empty"))
		}
		if typ != "" && strings.ContainsAny(payload, ",\n\r") {
			panicJs(vm, fmt.Errorf("payload must not contain commas or newlines: %q", payload))
		}
		policy := ""
		if !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			p, err := argString(call, 1, helperName(typ))
			if err != nil {
				panicJs(vm, err)
			}
			policy = strings.TrimSpace(p)
			if policy == "" {
				panicJs(vm, errors.New("policy must not be empty"))
			}
		}
		line := typ
		if typ == "" {
			// rule(inner, policy): strip a user policy from the inner form.
			parts := strings.Split(payload, ",")
			if len(parts) < 2 {
				panicJs(vm, fmt.Errorf("rule() expects an inner rule like %q", "DOMAIN-SUFFIX,x.com"))
			}
			if err := validateInnerRule(strings.Join(parts[:2], ",")); err != nil {
				panicJs(vm, err)
			}
			if len(parts) > 2 && parts[2] != "no-resolve" {
				panicJs(vm, errors.New("inner rules must not carry a policy — pass it as the second argument"))
			}
			payload = strings.Join(parts, ",")
			line = payload
		} else {
			if typ == "IP-CIDR" && strings.Contains(payload, ":") {
				line = "IP-CIDR6" // IPv6 literal — mihomo keeps the types separate
			}
			line += "," + payload
		}
		if policy != "" {
			line += "," + policy
		}
		if typ == "" || policy != "" {
			if err := ValidateRuleLine(line); err != nil {
				panicJs(vm, err)
			}
		} else if err := validateInnerRule(line); err != nil {
			panicJs(vm, err)
		}
		return vm.ToValue(line)
	}
}

// logicFunc builds and()/or()/not(). Members come from the inner-form
// helpers (or raw "TYPE,payload" strings); the policy is applied once,
// outside the group — exactly mihomo's AND,((...),(...)),POLICY shape.
func logicFunc(vm *goja.Runtime, op string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		members, err := argStringSlice(call.Argument(0), op)
		if err != nil {
			panicJs(vm, err)
		}
		if len(members) == 0 {
			panicJs(vm, errors.New(op+"() needs at least one rule"))
		}
		if op == "NOT" && len(members) > 1 {
			panicJs(vm, errors.New("NOT() takes exactly one rule (mihomo's NOT is unary)"))
		}
		// Policy is OPTIONAL: without it the group is an inner logic rule
		// that nests into another combinator (and([or([...]), ...], P)).
		policy := ""
		if !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			p, err := argString(call, 1, strings.ToLower(op))
			if err != nil {
				panicJs(vm, err)
			}
			policy = strings.TrimSpace(p)
			if policy == "" {
				panicJs(vm, errors.New("policy must not be empty"))
			}
		}
		var b strings.Builder
		b.WriteString(op)
		b.WriteString(",(")
		for i, m := range members {
			m = strings.TrimSpace(m)
			if err := validateInnerRule(m); err != nil {
				panicJs(vm, fmt.Errorf("%s() member %d: %w", op, i+1, err))
			}
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("(")
			b.WriteString(m)
			b.WriteString(")")
		}
		b.WriteString(")")
		if policy != "" {
			b.WriteString(",")
			b.WriteString(policy)
			if err := ValidateRuleLine(b.String()); err != nil {
				panicJs(vm, err)
			}
		} else if err := validateInnerRule(b.String()); err != nil {
			panicJs(vm, err)
		}
		return vm.ToValue(b.String())
	}
}

// helperName renders a helper name for error messages.
func helperName(typ string) string {
	if typ == "" {
		return "rule"
	}
	return typ
}

// panicJs raises a JS-visible error with a precise message.
func panicJs(vm *goja.Runtime, err error) {
	panic(vm.NewGoError(err))
}

// argString reads one string argument.
func argString(call goja.FunctionCall, idx int, fname string) (string, error) {
	v := call.Argument(idx)
	if goja.IsUndefined(v) || goja.IsNull(v) {
		return "", fmt.Errorf("%s: argument %d is required", fname, idx+1)
	}
	switch s := v.Export().(type) {
	case string:
		return s, nil
	case int64:
		return fmt.Sprintf("%d", s), nil
	case float64:
		if s == float64(int64(s)) {
			return fmt.Sprintf("%d", int64(s)), nil
		}
		return "", fmt.Errorf("%s: argument %d must be an integer or string (got %v)", fname, idx+1, s)
	default:
		return "", fmt.Errorf("%s: argument %d must be a string (got %v)", fname, idx+1, v.Export())
	}
}

// argStringSlice reads a string or array-of-strings argument (logic members).
func argStringSlice(v goja.Value, fname string) ([]string, error) {
	if goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, fmt.Errorf("%s: first argument (rules) is required", fname)
	}
	if s, ok := v.Export().(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("%s: rules must not be empty", fname)
		}
		return []string{s}, nil
	}
	arr, ok := v.Export().([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s: rules must be a string or an array of strings (got %T)", fname, v.Export())
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s: rules must be strings (got %T)", fname, item)
		}
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// ------------------------------------------------------------ validation --

// ValidateRuleLine checks one FINAL rule line's shape:
//
//	TYPE,payload[,policy][,no-resolve]   (plain rules)
//	MATCH,policy                         (catch-all)
//	OP,(group),policy                    (AND/OR/NOT — balanced parens)
//
// Deep semantics (payload grammar, group/target existence) are mihomo's
// job — this is layer one of the two-layer validation discipline.
func ValidateRuleLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return errors.New("empty rule")
	}
	if len(line) > MaxRuleLen {
		return fmt.Errorf("rule too long (max %d)", MaxRuleLen)
	}
	if strings.ContainsAny(line, "\n\r") {
		return errors.New("rule must be a single line")
	}
	op := line
	if i := strings.IndexByte(line, ','); i >= 0 {
		op = line[:i]
	}
	if !ruleTypes[op] {
		return fmt.Errorf("unknown rule type %q (see the mihomo rules docs)", op)
	}
	switch op {
	case "MATCH":
		if n := strings.Count(line, ","); n != 1 {
			return errors.New("MATCH takes exactly TYPE,policy")
		}
		if strings.SplitN(line, ",", 2)[1] == "" {
			return errors.New("MATCH policy must not be empty")
		}
		return nil
	case "AND", "OR", "NOT":
		return validateLogicLine(line)
	}
	parts := strings.Split(line, ",")
	if len(parts) < 3 {
		if len(parts) == 2 {
			return fmt.Errorf("missing policy — pass it to the helper (domainSuffix(%q, \"PROXY\")) or nest the rule in and()/or()/not()", parts[1])
		}
		return fmt.Errorf("rule needs TYPE,payload,policy (got %d fields)", len(parts))
	}
	if parts[1] == "" {
		return errors.New("payload must not be empty")
	}
	if parts[2] == "" {
		return errors.New("policy must not be empty")
	}
	if len(parts) == 4 && parts[3] != "no-resolve" {
		return fmt.Errorf("extra field %q (only no-resolve is allowed)", parts[3])
	}
	if len(parts) > 4 {
		return errors.New("too many fields (TYPE,payload,policy[,no-resolve])")
	}
	return nil
}

// validateInnerRule checks a rule nested inside AND/OR/NOT — inner rules
// carry no policy (mihomo applies the group's policy to the whole group).
func validateInnerRule(rule string) error {
	rule = strings.TrimSpace(rule)
	op := rule
	if i := strings.IndexByte(rule, ','); i >= 0 {
		op = rule[:i]
	}
	if !ruleTypes[op] {
		return fmt.Errorf("unknown rule type %q", op)
	}
	if op == "MATCH" {
		return errors.New("MATCH cannot nest inside a logic group")
	}
	if logicOps[op] {
		// Nested logic groups are legal inner members — but must be
		// policy-less: OP,(members).
		_, after, found := cutBalancedGroup(rule[strings.Index(rule, ",")+1:])
		if !found {
			return errors.New(op + " group parens are unbalanced")
		}
		if strings.TrimSpace(after) != "" {
			return errors.New("nested " + op + " must not carry a policy — pass it to the outer and()/or()/not()")
		}
		return nil
	}
	parts := strings.Split(rule, ",")
	if len(parts) < 2 || parts[1] == "" {
		return errors.New("inner rule needs TYPE,payload")
	}
	// Optional trailing parameters: no-resolve (IP rules). Anything else in
	// position 3+ is a user policy mistake — reject with guidance.
	for _, extra := range parts[2:] {
		if extra != "no-resolve" {
			return errors.New("inner rules must not carry a policy — pass it to and()/or()/not()")
		}
	}
	return nil
}

// validateLogicLine checks "OP,(group),policy" with balanced parentheses.
func validateLogicLine(line string) error {
	op, rest, _ := strings.Cut(line, ",")
	if !strings.HasPrefix(rest, "(") {
		return errors.New(op + " group must be parenthesized: " + op + ",((TYPE,payload),...),policy")
	}
	_, after, found := cutBalancedGroup(rest)
	if !found {
		return errors.New(op + " group parens are unbalanced")
	}
	policy := strings.TrimSpace(after)
	policy = strings.TrimPrefix(policy, ",")
	if policy == "" {
		return errors.New(op + " policy must not be empty")
	}
	if strings.ContainsAny(policy, ",()") {
		return errors.New(op + " policy must be a plain name after the group")
	}
	return nil
}

// cutBalancedGroup cuts "(...)" from the start of s (balancing nested
// parens; rule payloads never quote) and returns the inner text plus
// whatever follows the group.
func cutBalancedGroup(s string) (inner, rest string, found bool) {
	if !strings.HasPrefix(s, "(") {
		return "", s, false
	}
	depth := 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[1:i], s[i+1:], true
			}
		}
	}
	return "", s, false
}

// RulePolicy extracts the routing target (policy) of a final rule line for
// generation-time existence filtering — the same mechanism the routing
// overrides use so one stale target can never take the core config down.
// Returns "" when the shape is malformed or no policy exists.
func RulePolicy(line string) string {
	line = strings.TrimSpace(line)
	op, rest, _ := strings.Cut(line, ",")
	switch op {
	case "MATCH":
		p := strings.TrimSpace(rest)
		if p == "" || strings.Contains(p, ",") {
			return ""
		}
		return p
	case "AND", "OR", "NOT":
		if !strings.HasPrefix(rest, "(") {
			return ""
		}
		_, after, found := cutBalancedGroup(rest)
		if !found {
			return ""
		}
		p := strings.TrimSpace(after)
		p = strings.TrimPrefix(p, ",")
		if p == "" || strings.ContainsAny(p, ",()") {
			return ""
		}
		return p
	}
	parts := strings.Split(line, ",")
	if len(parts) < 3 || parts[2] == "" {
		return ""
	}
	return parts[2]
}

// SortedTypes lists the accepted rule types (frontend docs/tests).
func SortedTypes() []string {
	out := make([]string, 0, len(ruleTypes))
	for t := range ruleTypes {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
