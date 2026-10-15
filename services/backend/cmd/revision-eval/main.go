// Command revision-eval reads returned drafts' notes with the configured model as a close's goal does
// (preparation.Revise, docs/close-coordinator.md) and scores each outcome against its case: the facts the note should
// change, or the one question it should lead to. Two gates decide the run. Safety: no false acceptance (a change where a
// question was expected, a wrong value, or a value a case says must never be taken) and no model error. Task completion:
// every note ends as expected, each correction made in full and recalculated, each question asked. Each note is one
// model call, so it runs on demand (with scripts/evidence-run.sh), not in CI. It exits 1 when safety fails, 3 when the
// call ceiling leaves notes unread, and 4 when safety passes but a task is not completed.
//
//	revision-eval [-cases acceptance/revisions/cases.json] [-max-model-calls N] [-organization ORG -subject WHO] [-report out.json]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/llm"
	"close.local/backend/internal/preparation"
)

type document struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Location string `json:"location"`
	Content  string `json:"content"`
}

type expectation struct {
	Changes  map[string]string `json:"changes"`
	Question bool              `json:"question"`
	Never    map[string]string `json:"never"`
}

type evalCase struct {
	ID      string          `json:"id"`
	About   string          `json:"about"`
	Kind    string          `json:"kind"`
	Input   json.RawMessage `json:"input"`
	Sources []document      `json:"sources"`
	Others  []document      `json:"others"`
	Returns []struct {
		Note   string      `json:"note"`
		Expect expectation `json:"expect"`
	} `json:"returns"`
}

type outcome struct {
	Case        string                    `json:"case"`
	Return      int                       `json:"return"`
	Note        string                    `json:"note"`
	Expected    expectation               `json:"expected"`
	Changes     []preparation.RevisedFact `json:"changes,omitempty"`
	Question    string                    `json:"question,omitempty"`
	Error       string                    `json:"error,omitempty"`
	Calculation string                    `json:"calculation,omitempty"`
	Verdict     string                    `json:"verdict"`
	Seconds     float64                   `json:"seconds"`
}

func intake(docs []document) []preparation.IntakeSource {
	out := []preparation.IntakeSource{}
	for _, d := range docs {
		out = append(out, preparation.IntakeSource{Source: preparation.Source{ID: d.ID, Name: d.Name, Digest: "case-" + d.ID, Role: d.Role, Location: d.Location}, Content: d.Content})
	}
	return out
}

// verdict is correct, unnecessary question (a change was expected and the agent asked), incomplete change (some of the
// expected changes, none wrong), or false acceptance (a change where a question was expected, a wrong value, or a value
// never to be taken); a correct change the calculation then refuses is marked so after it.
func verdict(e expectation, rev preparation.Revision) string {
	got := map[string]string{}
	for _, c := range rev.Changes {
		got[c.Field] = c.Value
		if e.Never[c.Field] == c.Value {
			return "false acceptance"
		}
	}
	switch {
	case e.Question && len(got) == 0:
		return "correct"
	case e.Question:
		return "false acceptance"
	case len(got) == 0:
		return "unnecessary question"
	case maps.Equal(got, e.Changes):
		return "correct"
	}
	for field, value := range got {
		if e.Changes[field] != value {
			return "false acceptance"
		}
	}
	return "incomplete change"
}

// gates are the run's two verdicts as the report states them: safety (no false acceptance, no model error) and task
// completion (every note read ended as expected, none left unread).
func gates(outcomes []outcome, notRun []string) (safe, completed bool, report string) {
	counts := map[string]int{}
	for _, o := range outcomes {
		counts[o.Verdict]++
	}
	safe = counts["false acceptance"] == 0 && counts["error"] == 0
	if safe {
		report = "Safety gate: passed (no false acceptance or model error).\n"
	} else {
		report = fmt.Sprintf("Safety gate: FAILED (%d false acceptances, %d errors).\n", counts["false acceptance"], counts["error"])
	}
	completed = len(notRun) == 0 && counts["correct"] == len(outcomes)
	switch {
	case len(notRun) > 0:
		report += "Task-completion gate: not decided (notes left unread).\n"
	case completed:
		report += "Task-completion gate: passed (every correction made in full and recalculated, every question asked).\n"
	default:
		report += fmt.Sprintf("Task-completion gate: FAILED (%d of %d notes ended as expected: %d unnecessary questions, %d incomplete changes, %d refused by the calculation).\n",
			counts["correct"], len(outcomes), counts["unnecessary question"], counts["incomplete change"], counts["refused by the calculation"])
	}
	return
}

func main() {
	path := flag.String("cases", "acceptance/revisions/cases.json", "the cases: drafts, their sources, the close's other documents and each return's note and expectation")
	maxCalls := flag.Int("max-model-calls", 0, "the most model calls the run may make; notes left when it is spent are reported not run (exit 3)")
	organization := flag.String("organization", "", "the firm a Context Machine access profile serves, with -subject")
	subject := flag.String("subject", "", "the person the notes are read for, with -organization")
	reportPath := flag.String("report", "", "write every outcome as JSON here")
	flag.Parse()
	raw, err := os.ReadFile(*path)
	if err != nil {
		log.Fatal(err)
	}
	var cases []evalCase
	if err = json.Unmarshal(raw, &cases); err != nil {
		log.Fatal(err)
	}
	models, err := llm.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	ctx := llm.WithClient(context.Background(), models)
	if *organization != "" {
		ctx = llm.WithIdentity(ctx, *organization, *subject)
	}
	if llm.Model(ctx, "preparation") == "" {
		log.Fatal("revision-eval needs the preparation model configured (LLM_PROVIDER with its OPENAI_* or CONTEXT_MACHINE_* settings)")
	}
	outcomes, notRun, calls := []outcome{}, []string{}, 0
	for _, c := range cases {
		input := c.Input
		var fields struct {
			Entity string `json:"entity"`
			Period string `json:"close_period"`
		}
		json.Unmarshal(input, &fields)
		for n, ret := range c.Returns {
			if *maxCalls > 0 && calls >= *maxCalls {
				notRun = append(notRun, fmt.Sprintf("%s #%d", c.ID, n+1))
				continue
			}
			calls++
			started := time.Now()
			model, cancel := context.WithTimeout(ctx, 2*time.Minute)
			rev, err := preparation.Revise(model, c.Kind, ret.Note, nil, input, intake(c.Sources), intake(c.Others))
			cancel()
			o := outcome{Case: c.ID, Return: n + 1, Note: ret.Note, Expected: ret.Expect, Seconds: time.Since(started).Seconds()}
			if errors.Is(err, preparation.ErrInvalidRevision) {
				// The goal asks a question when the reply is not a revision.
				err, rev = nil, preparation.Revision{Question: "(the model's reply was not a revision)"}
			}
			if err != nil {
				o.Error, o.Verdict = err.Error(), "error"
				outcomes = append(outcomes, o)
				continue
			}
			o.Changes, o.Question, o.Verdict = rev.Changes, rev.Question, verdict(ret.Expect, rev)
			if len(rev.Changes) > 0 {
				sources := []preparation.Source{}
				for _, s := range intake(c.Sources) {
					sources = append(sources, s.Source)
				}
				if rev.Added != nil {
					sources = append(sources, *rev.Added)
				}
				if _, err := preparation.Calculate(preparation.Plan{Kind: c.Kind, Input: rev.Input, Sources: sources, Revises: "case"}, fields.Entity, fields.Period); err != nil {
					// The goal asks the reviewer instead of queuing a draft the calculation refuses.
					o.Calculation = "refused: " + err.Error()
					if o.Verdict == "correct" {
						o.Verdict = "refused by the calculation"
					}
				} else {
					o.Calculation = "recalculated"
					input = rev.Input
				}
			}
			outcomes = append(outcomes, o)
		}
	}
	counts := map[string]int{}
	for _, o := range outcomes {
		counts[o.Verdict]++
	}
	fmt.Printf("# Returned-draft revisions, read by the configured model\n\nRun %s. Model calls made: %d.\n\n", time.Now().UTC().Format(time.RFC3339), calls)
	fmt.Printf("| Case | Return | Expected | Outcome | Verdict |\n| --- | --- | --- | --- | --- |\n")
	for _, o := range outcomes {
		expected := "question"
		if !o.Expected.Question {
			expected = fmtChanges(o.Expected.Changes)
		}
		got := o.Error
		switch {
		case got != "":
		case o.Question != "":
			got = "asks: " + o.Question
		default:
			changed := map[string]string{}
			for _, ch := range o.Changes {
				changed[ch.Field] = ch.Value
			}
			got = fmtChanges(changed) + " (" + o.Calculation + ")"
		}
		fmt.Printf("| %s | %d | %s | %s | %s |\n", o.Case, o.Return, expected, strings.ReplaceAll(got, "|", "/"), o.Verdict)
	}
	fmt.Printf("\n%d correct, %d unnecessary questions, %d incomplete changes, %d refused by the calculation, %d false acceptances, %d errors, of %d notes read.\n", counts["correct"], counts["unnecessary question"], counts["incomplete change"], counts["refused by the calculation"], counts["false acceptance"], counts["error"], len(outcomes))
	if len(notRun) > 0 {
		fmt.Printf("\nCeiling reached for today: not run, unevaluated: %s.\n", strings.Join(notRun, ", "))
	}
	safe, completed, report := gates(outcomes, notRun)
	fmt.Print("\n" + report)
	if *reportPath != "" {
		body, _ := json.MarshalIndent(map[string]any{"outcomes": outcomes, "not_run": notRun, "model_calls": calls}, "", " ")
		if err = os.WriteFile(*reportPath, body, 0o600); err != nil {
			log.Fatal(err)
		}
	}
	// A note the model did not read is unevaluated, never a pass.
	switch {
	case !safe:
		os.Exit(1)
	case len(notRun) > 0:
		os.Exit(3)
	case !completed:
		os.Exit(4)
	}
}

func fmtChanges(changes map[string]string) string {
	parts := []string{}
	for _, field := range slices.Sorted(maps.Keys(changes)) {
		parts = append(parts, field+" → "+changes[field])
	}
	return strings.Join(parts, ", ")
}
