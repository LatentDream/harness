package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"latentdream/harness/internal/runtime/command"
)

func findFZF() (string, error) {
	path, err := exec.LookPath("fzf")
	if err != nil {
		return "", errors.New("fzf is required for interactive file and command selection; install fzf and ensure it is on PATH")
	}
	return path, nil
}

func fileCandidates(ctx context.Context, root string) ([]string, error) {
	git := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if output, err := git.Output(); err == nil {
		return cleanCandidates(strings.Split(string(output), "\x00")), nil
	}

	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list files for picker: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func commandCandidates(registry *command.Registry, prefix string) []string {
	entries := registry.Entries()
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		mapping := ""
		for _, candidate := range entry.Mappings {
			if strings.HasPrefix(candidate, prefix) {
				mapping = candidate
				break
			}
		}
		if mapping == "" && len(entry.Mappings) > 0 {
			mapping = entry.Mappings[0]
		}
		if mapping != "" {
			result = append(result, mapping+"\t"+entry.Description)
		}
	}
	return result
}

func cleanCandidates(values []string) []string {
	clean := values[:0]
	for _, value := range values {
		if value != "" {
			clean = append(clean, filepath.ToSlash(value))
		}
	}
	sort.Strings(clean)
	return clean
}

func runFZF(ctx context.Context, executable string, candidates []string, prompt string, query string, zeroDelimited bool, errWriter *os.File) (string, bool, error) {
	if len(candidates) == 0 {
		return "", false, nil
	}
	arguments := []string{
		"--height=60%", "--layout=reverse", "--border", "--prompt=" + prompt,
		"--no-multi", "--cycle", "--query=" + query,
	}
	separator := "\n"
	if zeroDelimited {
		arguments = append(arguments, "--read0", "--print0")
		separator = "\x00"
	} else {
		arguments = append(arguments, "--delimiter=\t", "--with-nth=1,2")
	}
	cmd := exec.CommandContext(ctx, executable, arguments...)
	cmd.Stdin = strings.NewReader(strings.Join(candidates, separator) + separator)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = errWriter
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 130) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("run fzf: %w", err)
	}
	selected := output.String()
	if zeroDelimited {
		selected = strings.TrimSuffix(selected, "\x00")
	} else {
		selected = strings.TrimSuffix(selected, "\n")
		selected = strings.TrimSuffix(selected, "\r")
	}
	if selected == "" {
		return "", false, nil
	}
	if !zeroDelimited {
		selected, _, _ = strings.Cut(selected, "\t")
	}
	return selected, true, nil
}
