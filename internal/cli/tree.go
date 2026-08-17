package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"dockvault/internal/backup"
	"dockvault/internal/rclone"
	"dockvault/internal/workspace"
)

func init() {
	register(&command{
		name:    "tree",
		summary: "Show the remote Google Drive backup folder as a tree",
		run:     runTree,
	})
}

// runTree shells out to internal/rclone (ListTree, backed by
// `rclone lsjson --recursive`) to render the remote backup folder
// structure, optionally annotated with sizes/dates (--detailed).
func runTree(ctx context.Context, g Global, args []string) error {
	fs := flag.NewFlagSet("tree", flag.ContinueOnError)
	detailed := fs.Bool("detailed", false, "show file sizes, dates, latest backup per leaf")
	fs.BoolVar(detailed, "d", false, "shorthand for --detailed")
	output := fs.String("output", "text", "output format: json|text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *output != "json" && *output != "text" {
		return fmt.Errorf("--output must be \"json\" or \"text\", got %q", *output)
	}

	root := "/"
	if rest := fs.Args(); len(rest) > 0 {
		root = rest[0]
	}

	ws, err := workspace.Open(g.Home)
	if err != nil {
		return err
	}
	cfg, err := ws.LoadConfig()
	if err != nil {
		return err
	}
	rc := rclone.New(resolveRcloneRemote(g, cfg))
	if err := rc.CheckConfigured(ctx); err != nil {
		return err
	}

	entries, err := rc.ListTree(ctx, root)
	if err != nil {
		return err
	}

	if *output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}

	if len(entries) == 0 {
		fmt.Printf("%s (empty)\n", root)
		return nil
	}
	fmt.Println(root)
	printTree(os.Stdout, buildTree(entries), "", *detailed)
	return nil
}

// treeNode is one path segment in the tree built from a flat
// []rclone.TreeEntry - directories have children, files don't.
type treeNode struct {
	isDir    bool
	size     int64
	modTime  string
	children map[string]*treeNode
}

func buildTree(entries []rclone.TreeEntry) *treeNode {
	root := &treeNode{isDir: true, children: map[string]*treeNode{}}
	for _, e := range entries {
		parts := strings.Split(strings.Trim(e.Path, "/"), "/")
		cur := root
		for i, part := range parts {
			if part == "" {
				continue
			}
			child, ok := cur.children[part]
			if !ok {
				child = &treeNode{children: map[string]*treeNode{}}
				cur.children[part] = child
			}
			if i == len(parts)-1 {
				child.isDir = e.IsDir
				child.size = e.Size
				child.modTime = e.ModTime
			} else {
				child.isDir = true
			}
			cur = child
		}
	}
	return root
}

func printTree(w io.Writer, node *treeNode, prefix string, detailed bool) {
	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		child := node.children[name]
		last := i == len(names)-1
		branch, nextPrefix := "├── ", prefix+"│   "
		if last {
			branch, nextPrefix = "└── ", prefix+"    "
		}

		line := name
		if detailed && !child.isDir {
			line += fmt.Sprintf("  (%s, %s)", backup.FormatBytes(child.size), child.modTime)
		}
		fmt.Fprintf(w, "%s%s%s\n", prefix, branch, line)
		if len(child.children) > 0 {
			printTree(w, child, nextPrefix, detailed)
		}
	}
}
