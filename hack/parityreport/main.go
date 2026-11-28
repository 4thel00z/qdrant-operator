package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

type event struct {
	Action  string  `json:"Action"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

type outcome struct {
	action  string
	elapsed float64
}

type implementation struct {
	name     string
	outcomes map[string]outcome
	order    []string
	setup    bool
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(paths []string, out io.Writer) error {
	if len(paths) == 0 {
		return errors.New("usage: parityreport <impl>=<results.json> ...")
	}
	implementations := []implementation{}
	for _, path := range paths {
		name, file, found := strings.Cut(path, "=")
		if !found {
			return fmt.Errorf("argument %q: want <impl>=<results.json>", path)
		}
		loaded, err := load(name, file)
		if err != nil {
			return err
		}
		implementations = append(implementations, loaded)
	}
	render(out, implementations)
	return nil
}

func load(name, path string) (implementation, error) {
	file, err := os.Open(path)
	if err != nil {
		return implementation{}, fmt.Errorf("open results of %s: %w", name, err)
	}
	defer file.Close()
	loaded := implementation{name: name, outcomes: map[string]outcome{}}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	packageFailed := false
	for scanner.Scan() {
		var entry event
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Test == "" {
			packageFailed = packageFailed || entry.Action == "fail"
			continue
		}
		if entry.Action == "run" && !strings.Contains(entry.Test, "/") {
			loaded.order = append(loaded.order, entry.Test)
		}
		if slices.Contains([]string{"pass", "fail", "skip"}, entry.Action) {
			loaded.outcomes[entry.Test] = outcome{action: entry.Action, elapsed: entry.Elapsed}
		}
	}
	if err := scanner.Err(); err != nil {
		return implementation{}, fmt.Errorf("read results of %s: %w", name, err)
	}
	loaded.setup = packageFailed && len(loaded.outcomes) == 0
	return loaded, nil
}

func scenarios(implementations []implementation) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, impl := range implementations {
		for _, name := range impl.order {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func cell(impl implementation, name string) string {
	result, ok := impl.outcomes[name]
	if !ok {
		return "not run"
	}
	return fmt.Sprintf("%s (%s)", result.action, duration(result.elapsed))
}

func duration(seconds float64) string {
	if seconds >= 60 {
		return fmt.Sprintf("%dm%02ds", int(seconds)/60, int(seconds)%60)
	}
	return fmt.Sprintf("%.1fs", seconds)
}

func render(out io.Writer, implementations []implementation) {
	header := []string{"scenario"}
	divider := []string{"---"}
	for _, impl := range implementations {
		header = append(header, impl.name)
		divider = append(divider, "---")
	}
	fmt.Fprintf(out, "| %s |\n| %s |\n", strings.Join(header, " | "), strings.Join(divider, " | "))
	names := scenarios(implementations)
	for _, name := range names {
		row := []string{name}
		for _, impl := range implementations {
			row = append(row, cell(impl, name))
		}
		fmt.Fprintf(out, "| %s |\n", strings.Join(row, " | "))
	}
	totals := []string{"**total passed**"}
	for _, impl := range implementations {
		totals = append(totals, fmt.Sprintf("%d / %d", count(impl, "pass"), len(names)))
	}
	fmt.Fprintf(out, "| %s |\n", strings.Join(totals, " | "))
	for _, impl := range implementations {
		fmt.Fprintf(out, "\n%s: %d pass, %d fail, %d skip", impl.name, count(impl, "pass"), count(impl, "fail"), count(impl, "skip"))
		if impl.setup {
			fmt.Fprint(out, " (suite setup failed before any scenario ran)")
		}
	}
	fmt.Fprintln(out)
}

func count(impl implementation, action string) int {
	total := 0
	for name, result := range impl.outcomes {
		if result.action == action && !strings.Contains(name, "/") {
			total++
		}
	}
	return total
}
