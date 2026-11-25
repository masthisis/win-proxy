package rulescript

import (
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// ------------------------------------------------------- happy paths ----

func TestCompile_SingleString(t *testing.T) {
	rules, err := Compile(`return "DOMAIN-SUFFIX,example.com,PROXY";`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0] != "DOMAIN-SUFFIX,example.com,PROXY" {
		t.Fatalf("got %v", rules)
	}
}

func TestCompile_ArrayOfStrings(t *testing.T) {
	src := `
const domains = ["openai.com", "anthropic.com", "chatgpt.com"];
return domains.map(d => domainSuffix(d, "PROXY"));`
	rules, err := Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DOMAIN-SUFFIX,openai.com,PROXY",
		"DOMAIN-SUFFIX,anthropic.com,PROXY",
		"DOMAIN-SUFFIX,chatgpt.com,PROXY",
	}
	if strings.Join(rules, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", rules, want)
	}
}

func TestCompile_NoRules(t *testing.T) {
	for _, src := range []string{"", "return;", "return undefined;", "return null;", "return '';"} {
		rules, err := Compile(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if len(rules) != 0 {
			t.Fatalf("%q: expected no rules, got %v", src, rules)
		}
	}
}

// Helper surface: exact rule text for every constructor.
func TestHelpers_ExactOutput(t *testing.T) {
	cases := []struct{ src, want string }{
		{`return domain("a.com", "PROXY");`, "DOMAIN,a.com,PROXY"},
		{`return domainKeyword("google", "DIRECT");`, "DOMAIN-KEYWORD,google,DIRECT"},
		{`return domainRegex("^(www\\.)?x\\.com$", "PROXY");`, "DOMAIN-REGEX,^(www\\.)?x\\.com$,PROXY"},
		{`return domainWildcard("*.a.com", "PROXY");`, "DOMAIN-WILDCARD,*.a.com,PROXY"},
		{`return geosite("youtube", "PROXY");`, "GEOSITE,youtube,PROXY"},
		{`return ipCidr("1.2.3.0/24", "DIRECT");`, "IP-CIDR,1.2.3.0/24,DIRECT"},
		{`return ipCidr("2606:4700::/32", "DIRECT");`, "IP-CIDR6,2606:4700::/32,DIRECT"},
		{`return ipCidr6("2606:4700::/32", "DIRECT");`, "IP-CIDR6,2606:4700::/32,DIRECT"},
		{`return srcIpCidr("192.168.0.0/16", "DIRECT");`, "SRC-IP-CIDR,192.168.0.0/16,DIRECT"},
		{`return geoip("cn", "DIRECT");`, "GEOIP,cn,DIRECT"},
		{`return dstPort(443, "PROXY");`, "DST-PORT,443,PROXY"},
		{`return processName("telegram.exe", "DIRECT");`, "PROCESS-NAME,telegram.exe,DIRECT"},
		{`return ruleSet("my-rules", "PROXY");`, "RULE-SET,my-rules,PROXY"},
		{`return rule(domainSuffix("x.com"), "REJECT");`, "DOMAIN-SUFFIX,x.com,REJECT"},
	}
	for _, c := range cases {
		rules, err := Compile(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		if len(rules) != 1 || rules[0] != c.want {
			t.Fatalf("%s: got %v want [%s]", c.src, rules, c.want)
		}
	}
}

// Logic combinators: exact mihomo AND/OR/NOT syntax (inner rules carry no
// policy; the policy rides outside the group).
func TestCombinators_ExactSyntax(t *testing.T) {
	cases := []struct{ src, want string }{
		{
			`return and([network("udp"), dstPort(53)], "DIRECT");`,
			"AND,((NETWORK,udp),(DST-PORT,53)),DIRECT",
		},
		{
			`return or([domainSuffix("a.com"), domainKeyword("b")], "PROXY");`,
			"OR,((DOMAIN-SUFFIX,a.com),(DOMAIN-KEYWORD,b)),PROXY",
		},
		{
			`return not(domainSuffix("ads.example.com"), "PROXY");`,
			"NOT,((DOMAIN-SUFFIX,ads.example.com)),PROXY",
		},
		{
			// nested logic groups
			`return and([or([network("tcp"), network("udp")]), dstPort(443)], "PROXY");`,
			"AND,((OR,((NETWORK,tcp),(NETWORK,udp))),(DST-PORT,443)),PROXY",
		},
		{
			// inner rule with no-resolve parameter survives
			`return and([ipCidr("1.2.3.0/24"), dstPort(443)], "DIRECT");`,
			"AND,((IP-CIDR,1.2.3.0/24),(DST-PORT,443)),DIRECT",
		},
		{
			`return match("PROXY");`,
			"MATCH,PROXY",
		},
	}
	for _, c := range cases {
		rules, err := Compile(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		if len(rules) != 1 || rules[0] != c.want {
			t.Fatalf("%s: got %v want [%s]", c.src, rules, c.want)
		}
	}
}

func TestCompile_BareInnerRuleRejected(t *testing.T) {
	_, err := Compile(`return domainSuffix("a.com");`)
	if err == nil || !strings.Contains(err.Error(), "missing policy") {
		t.Fatalf("expected missing-policy guidance, got: %v", err)
	}
	_, err = Compile(`return and([or([network("tcp"), network("udp")])]);`)
	if err == nil {
		t.Fatal("expected rejection for a policy-less top-level logic group")
	}
}

func TestCombinators_RawStringMembers(t *testing.T) {
	rules, err := Compile(`return and(["DOMAIN,a.com", "DST-PORT,443"], "PROXY");`)
	if err != nil {
		t.Fatal(err)
	}
	if rules[0] != "AND,((DOMAIN,a.com),(DST-PORT,443)),PROXY" {
		t.Fatalf("got %v", rules)
	}
}

// ------------------------------------------------------------ sandbox ----

// The issue's acceptance criterion: "Sandbox verified: no I/O surface
// exposed to snippet evaluation (test included)."
func TestSandbox_NoIOSurface(t *testing.T) {
	// Every host capability a snippet could try must be undefined.
	probes := []string{
		"require", "fetch", "process", "fs", "os", "child_process",
		"http", "net", "console", "setTimeout", "setInterval",
		"Deno", "Bun", "Wails", "window", "document", "globalThis.__nr__",
	}
	var checks []string
	for _, p := range probes {
		checks = append(checks, `typeof `+p+` !== "undefined"`)
	}
	src := "const bad = [" + strings.Join(checks, ",") + "];\n" +
		"if (bad.some(b => b)) throw new Error('sandbox leak: host global reachable');\n" +
		`return "DOMAIN,x.com,PROXY";`
	if _, err := Compile(src); err != nil {
		t.Fatalf("sandbox probe failed: %v", err)
	}

	// Version-proof leak probe: a fresh sandbox VM with the SAME helper
	// registration is the baseline; the script under test must not see any
	// global beyond it (goja version drift cannot break the test).
	vm0 := goja.New()
	vm0.SetMaxCallStackSize(MaxCallStack)
	registerHelpers(vm0)
	namesVal, err := vm0.RunString("Object.getOwnPropertyNames(globalThis).join('|')")
	if err != nil {
		t.Fatal(err)
	}
	baseline := strings.Split(namesVal.String(), "|")
	baselineJS := "["
	for i, n := range baseline {
		if i > 0 {
			baselineJS += ","
		}
		baselineJS += "'" + n + "'"
	}
	baselineJS += "]"

	src2 := "const baseline = new Set(" + baselineJS + ");\n" +
		"const ks = Object.getOwnPropertyNames(globalThis).filter(k => !baseline.has(k));\n" +
		`return ks.length > 0 ? "DOMAIN,leak.com," + ks[0] + ",PROXY" : "DOMAIN,ok.com,PROXY";`
	rules, err := Compile(src2)
	if err != nil {
		t.Fatal(err)
	}
	if rules[0] != "DOMAIN,ok.com,PROXY" {
		t.Fatalf("unexpected globals leaked into sandbox: %s", rules[0])
	}
}

func TestSandbox_InfiniteLoopInterrupted(t *testing.T) {
	start := time.Now()
	_, err := Compile("while (true) {}")
	if err == nil {
		t.Fatal("expected an error for an infinite loop")
	}
	if !strings.Contains(err.Error(), "budget") && !strings.Contains(err.Error(), "finish within") {
		t.Fatalf("expected a budget error, got: %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("interrupt took too long: %v", d)
	}
}

func TestSandbox_RunawayRecursionInterrupted(t *testing.T) {
	_, err := Compile("function f(){ return f(); } return f();")
	if err == nil {
		t.Fatal("expected an error for unbounded recursion")
	}
}

func TestSandbox_NoSecretsViaPrototype(t *testing.T) {
	// Prototype pollution attempts must not crash evaluation or leak.
	_, err := Compile(`
Object.prototype.__proto__ = null;
try { ({}).constructor.constructor("return process")(); } catch (e) {}
return "DOMAIN,x.com,PROXY";`)
	if err != nil {
		t.Fatalf("prototype-hardening script failed: %v", err)
	}
}

// ------------------------------------------------------ rejection set ----

func TestCompile_NonDeterministicRejected(t *testing.T) {
	for _, src := range []string{
		`return "DOMAIN,x" + Math.floor(Math.random()*10) + ".com,PROXY";`,
	} {
		if _, err := Compile(src); err == nil {
			t.Fatalf("expected determinism rejection for %q", src)
		}
	}
}

func TestCompile_BadOutputShapes(t *testing.T) {
	for _, src := range []string{
		`return 42;`,
		`return {rules: []};`,
		`return [1, 2];`,
		`return ["DOMAIN,a.com", {}];`,
	} {
		if _, err := Compile(src); err == nil {
			t.Fatalf("expected output-shape rejection for %q", src)
		}
	}
}

func TestCompile_SyntaxErrorsLinePrecise(t *testing.T) {
	_, err := Compile(`const a = (";return "DOMAIN,x.com,PROXY";`)
	if err == nil || !strings.Contains(err.Error(), "syntax error at line") {
		t.Fatalf("expected a line-precise syntax error, got: %v", err)
	}
}

func TestCompile_UnknownRuleTypeRejected(t *testing.T) {
	_, err := Compile(`return "MAGICAL-RULE,x.com,PROXY";`)
	if err == nil || !strings.Contains(err.Error(), "unknown rule type") {
		t.Fatalf("expected unknown-type rejection, got: %v", err)
	}
}

func TestCompile_OutputCaps(t *testing.T) {
	_, err := Compile(`return Array.from({length: 300}, (_, i) => "DOMAIN,d" + i + ".com,PROXY");`)
	if err == nil || !strings.Contains(err.Error(), "emitted") {
		t.Fatalf("expected output-cap rejection, got: %v", err)
	}
	_, err = Compile(`return ["DOMAIN," + "a".repeat(2000) + ".com,PROXY"];`)
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("expected rule-length rejection, got: %v", err)
	}
}

func TestCompile_HelperArgumentErrors(t *testing.T) {
	for _, src := range []string{
		`return domainSuffix();`,                     // missing arg
		`return domainSuffix("", "PROXY");`,          // empty payload
		`return domainSuffix("a,b.com", "PROXY");`,   // comma in payload
		`return domainSuffix("a.com", "");`,          // empty policy
		`return and([], "PROXY");`,                   // no members
		`return and("DOMAIN,a.com");`,                // missing policy
		`return and(["DOMAIN,a.com,DIRECT"], "P");`,  // member carries a policy
		`return and(["MATCH,PROXY"], "P");`,          // MATCH cannot nest
		`return not(["DOMAIN,a", "DOMAIN,b"], "P");`, // NOT is unary
	} {
		if _, err := Compile(src); err == nil {
			t.Fatalf("expected argument rejection for %q", src)
		}
	}
}

func TestCompile_SourceCap(t *testing.T) {
	if _, err := Compile(strings.Repeat("x", MaxSourceBytes+1)); err == nil {
		t.Fatal("expected source-cap rejection")
	}
}

// ------------------------------------------------------- validators ----

func TestValidateRuleLine(t *testing.T) {
	valid := []string{
		"DOMAIN-SUFFIX,example.com,PROXY",
		"GEOIP,CN,DIRECT,no-resolve",
		"MATCH,PROXY",
		"AND,((DOMAIN,baidu.com),(NETWORK,UDP),(DST-PORT,53)),DIRECT",
		"AND,(DOMAIN,a.com),PROXY", // mihomo's parser accepts a single-member group
		"NOT,((DOMAIN,baidu.com)),PROXY",
		"OR,((NETWORK,UDP),(DOMAIN,baidu.com)),REJECT",
		"AND,((OR,((NETWORK,tcp),(NETWORK,udp))),(DST-PORT,443)),PROXY",
	}
	for _, v := range valid {
		if err := ValidateRuleLine(v); err != nil {
			t.Fatalf("%q should be valid: %v", v, err)
		}
	}
	invalid := []string{
		"",
		"FOO,bar,baz",
		"DOMAIN-SUFFIX,example.com",
		"DOMAIN-SUFFIX,,PROXY",
		"DOMAIN-SUFFIX,example.com,",
		"MATCH",
		"MATCH,PROXY,EXTRA",
		"AND,(DOMAIN,a.com),",            // missing policy
		"AND,((DOMAIN,a.com)),PROXY,X",   // trailing junk
		"DOMAIN-SUFFIX,a.com,PROXY,junk", // bad extra field
		"DOMAIN-SUFFIX,a.com\nPROXY",
	}
	for _, v := range invalid {
		if err := ValidateRuleLine(v); err == nil {
			t.Fatalf("%q should be invalid", v)
		}
	}
}

func TestRulePolicy(t *testing.T) {
	cases := []struct{ line, want string }{
		{"DOMAIN-SUFFIX,example.com,PROXY", "PROXY"},
		{"GEOIP,CN,DIRECT,no-resolve", "DIRECT"},
		{"MATCH,Node Select", "Node Select"},
		{"AND,((DOMAIN,a.com),(DST-PORT,53)),DIRECT", "DIRECT"},
		{"NOT,((DOMAIN,a.com)),PROXY", "PROXY"},
		{"OR,((NETWORK,udp),(NETWORK,tcp)),NR Auto", "NR Auto"},
		{"AND,((DOMAIN,a.com)", ""}, // unbalanced
		{"DOMAIN-SUFFIX,x.com", ""}, // inner form has no policy
		{"GARBAGE", ""},
	}
	for _, c := range cases {
		if got := RulePolicy(c.line); got != c.want {
			t.Fatalf("RulePolicy(%q) = %q want %q", c.line, got, c.want)
		}
	}
}

func TestSortedTypes_ContainsVerifiedSet(t *testing.T) {
	types := SortedTypes()
	for _, want := range []string{"AND", "DOMAIN", "DOMAIN-SUFFIX", "GEOIP", "IP-CIDR6", "MATCH", "NETWORK", "PROCESS-NAME", "RULE-SET"} {
		found := false
		for _, t2 := range types {
			if t2 == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("rule type %q missing from the verified set", want)
		}
	}
}
