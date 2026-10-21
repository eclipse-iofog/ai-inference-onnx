package discover

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestKnowledgeNamesNothing(t *testing.T) {
	t.Parallel()

	t.Run("absent file", func(t *testing.T) {
		t.Parallel()
		model := filepath.Join(t.TempDir(), "detector")
		if err := os.MkdirAll(model, 0o755); err != nil {
			t.Fatal(err)
		}

		got := ReadKnowledge(model, t.TempDir())
		requireNamesNothing(t, got)
		if got.Fingerprint.Present {
			t.Fatalf("fingerprint %+v", got.Fingerprint)
		}
		again := ReadKnowledge(model, t.TempDir())
		if !got.Fingerprint.Equal(again.Fingerprint) {
			t.Fatalf("fingerprint %+v vs %+v", got.Fingerprint, again.Fingerprint)
		}
	})

	t.Run("blanks and comments", func(t *testing.T) {
		t.Parallel()
		model := filepath.Join(t.TempDir(), "detector")
		writeFile(t, filepath.Join(model, "knowledge"), "\n  \n# comment\n\t# also\n")

		got := ReadKnowledge(model, t.TempDir())
		requireNamesNothing(t, got)
		requireNameFile(t, got, filepath.Join(model, "knowledge"))
	})

	t.Run("name file inside a version directory", func(t *testing.T) {
		t.Parallel()
		model := filepath.Join(t.TempDir(), "detector")
		writeFile(t, filepath.Join(model, "1", "knowledge"), "labels\n")

		got := ReadKnowledge(model, t.TempDir())
		requireNamesNothing(t, got)
		if got.Fingerprint.Present {
			t.Fatal("version directory name file was read")
		}
	})
}

func TestKnowledgePresent(t *testing.T) {
	t.Parallel()

	t.Run("one directory", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		if err := os.MkdirAll(filepath.Join(knowledge, "labels"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(model, "knowledge"), "labels\n")

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReady(t, got, "labels")
		requireNameFile(t, got, filepath.Join(model, "knowledge"))
	})

	t.Run("two lines", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		if err := os.MkdirAll(filepath.Join(knowledge, "tokenizer"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(knowledge, "labels"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(model, "knowledge"), "tokenizer\nlabels\n")

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReady(t, got, "tokenizer", "labels")
	})

	t.Run("duplicate line counts once", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		if err := os.MkdirAll(filepath.Join(knowledge, "labels"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(model, "knowledge"), "labels\n  labels  \nlabels\n")

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReady(t, got, "labels")
	})

	t.Run("duplicate missing name counts once", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		writeFile(t, filepath.Join(model, "knowledge"), "labels\nlabels\n")

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReason(t, got, `knowledge "labels" is missing`)
		if len(got.Names) != 1 || len(got.Missing) != 1 || got.Missing[0] != "labels" {
			t.Fatalf("%+v", got)
		}
	})
}

func TestKnowledgeNotReady(t *testing.T) {
	t.Parallel()

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		writeFile(t, filepath.Join(model, "knowledge"), "labels\n")

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReason(t, got, `knowledge "labels" is missing`)
		if len(got.Missing) != 1 || got.Missing[0] != "labels" {
			t.Fatalf("missing %+v", got.Missing)
		}
		if len(got.Fingerprint.Exists) != 1 || got.Fingerprint.Exists[0] {
			t.Fatalf("presence %+v", got.Fingerprint.Exists)
		}
	})

	t.Run("not a directory", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		writeFile(t, filepath.Join(knowledge, "labels"), "text")
		writeFile(t, filepath.Join(model, "knowledge"), "labels\n")

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReason(t, got, `knowledge "labels" is not a directory`)
		if len(got.Missing) != 1 || got.Missing[0] != "labels" {
			t.Fatalf("missing %+v", got.Missing)
		}
	})

	t.Run("each missing name", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		writeFile(t, filepath.Join(knowledge, "weights"), "file")
		writeFile(t, filepath.Join(model, "knowledge"), "labels\nweights\n")

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReason(t, got, `knowledge "labels" is missing, knowledge "weights" is not a directory`)
		if len(got.Missing) != 2 || got.Missing[0] != "labels" || got.Missing[1] != "weights" {
			t.Fatalf("missing %+v", got.Missing)
		}
	})

	t.Run("rejected names", func(t *testing.T) {
		t.Parallel()
		names := []string{"a/b", `a\b`, "a*b", "a>b", ".", ".."}
		for _, name := range names {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				model, knowledge := knowledgeDirs(t)
				writeFile(t, filepath.Join(model, "knowledge"), name+"\n")

				got := ReadKnowledge(model, knowledge)
				requireKnowledgeReason(t, got, fmt.Sprintf("knowledge name %q is rejected", name))
				if len(got.Names) != 0 || len(got.Missing) != 0 {
					t.Fatalf("%+v", got)
				}
			})
		}
	})

	t.Run("name file is not valid text", func(t *testing.T) {
		t.Parallel()
		model, _ := knowledgeDirs(t)
		writeBytes(t, filepath.Join(model, "knowledge"), []byte{0xff, 0xfe})

		got := ReadKnowledge(model, t.TempDir())
		requireKnowledgeReason(t, got, "knowledge name file is not valid UTF-8")
	})
}

func TestKnowledgeUnavailable(t *testing.T) {
	t.Parallel()

	t.Run("unreadable name file", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		path := filepath.Join(model, "knowledge")
		writeFile(t, path, "labels\n")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0o644); err != nil {
				t.Errorf("restore mode: %v", err)
			}
		})

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReason(t, got, "knowledge name file could not be read")
		requireNameFile(t, got, path)
	})

	t.Run("directory where the name file should be", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		path := filepath.Join(model, "knowledge")
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReason(t, got, "knowledge name file is a directory")
		requireNameFile(t, got, path)
		if len(got.Names) != 0 {
			t.Fatalf("names %+v", got.Names)
		}
	})

	t.Run("named directory could not be read", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		if err := os.MkdirAll(filepath.Join(knowledge, "labels"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(model, "knowledge"), "labels\n")
		if err := os.Chmod(knowledge, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(knowledge, 0o755); err != nil {
				t.Errorf("restore mode: %v", err)
			}
		})

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReason(t, got, `knowledge "labels" could not be read`)
	})
}

func TestKnowledgeFingerprint(t *testing.T) {
	t.Parallel()
	model, knowledge := knowledgeDirs(t)
	item := filepath.Join(knowledge, "labels")
	if err := os.MkdirAll(item, 0o755); err != nil {
		t.Fatal(err)
	}
	nameFile := filepath.Join(model, "knowledge")
	writeFile(t, nameFile, "labels\n")
	interior := filepath.Join(item, "vocab.json")
	writeFile(t, interior, "before")

	before := ReadKnowledge(model, knowledge)
	requireKnowledgeReady(t, before, "labels")
	requireNameFile(t, before, nameFile)

	writeFile(t, interior, strings.Repeat("after", 1000))
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(interior, future, future); err != nil {
		t.Fatal(err)
	}
	after := ReadKnowledge(model, knowledge)
	if !before.Fingerprint.Equal(after.Fingerprint) {
		t.Fatalf("interior edit changed fingerprint: %+v vs %+v", before.Fingerprint, after.Fingerprint)
	}
	if strings.Contains(fmt.Sprintf("%#v", after), "vocab.json") {
		t.Fatalf("interior name recorded: %#v", after)
	}
	requireNameFile(t, after, nameFile)

	writeFile(t, nameFile, "labels\n# note\n")
	edited := ReadKnowledge(model, knowledge)
	requireKnowledgeReady(t, edited, "labels")
	if before.Fingerprint.Equal(edited.Fingerprint) {
		t.Fatal("name file edit left the fingerprint unchanged")
	}
	requireNameFile(t, edited, nameFile)

	if err := os.RemoveAll(item); err != nil {
		t.Fatal(err)
	}
	removed := ReadKnowledge(model, knowledge)
	requireKnowledgeReason(t, removed, `knowledge "labels" is missing`)
	if edited.Fingerprint.Equal(removed.Fingerprint) {
		t.Fatal("removing a named directory left the fingerprint unchanged")
	}
	if len(removed.Fingerprint.Exists) != 1 || removed.Fingerprint.Exists[0] {
		t.Fatalf("presence %+v", removed.Fingerprint.Exists)
	}
}

func TestKnowledgeDoesNotListModels(t *testing.T) {
	t.Parallel()

	t.Run("onnx file inside a knowledge directory", func(t *testing.T) {
		t.Parallel()
		model, knowledge := knowledgeDirs(t)
		item := filepath.Join(knowledge, "labels")
		const interior = "not-a-selected-model.onnx"
		writeFile(t, filepath.Join(item, interior), "weights")
		writeFile(t, filepath.Join(item, "config.json"), "{}")
		writeFile(t, filepath.Join(model, "knowledge"), "labels\n")
		if err := os.Chmod(item, 0o111); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(item, 0o755); err != nil {
				t.Errorf("restore mode: %v", err)
			}
		})

		got := ReadKnowledge(model, knowledge)
		requireKnowledgeReady(t, got, "labels")
		if strings.Contains(fmt.Sprintf("%#v", got), interior) {
			t.Fatalf("model path returned: %#v", got)
		}
	})

	t.Run("empty models root", func(t *testing.T) {
		t.Parallel()
		models := t.TempDir()
		knowledge := t.TempDir()
		touch(t, filepath.Join(knowledge, "corpus", "model.onnx"))

		got := mustResolve(t, models)
		if len(got) != 0 {
			t.Fatalf("models %+v", got)
		}
	})

	t.Run("files beside the onnx file", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "model.onnx"))
		vocab := filepath.Join(dir, "vocab.json")
		cfg := filepath.Join(dir, "config.json")
		writeFile(t, vocab, "{")
		writeFile(t, cfg, "{")
		if err := os.Chmod(vocab, 0); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(cfg, 0); err != nil {
			t.Fatal(err)
		}

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "1", filepath.Join(dir, "model.onnx"), false)
	})
}

func knowledgeDirs(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	return filepath.Join(root, "detector"), filepath.Join(root, "knowledge")
}

func requireNamesNothing(t *testing.T, got Knowledge) {
	t.Helper()
	if !got.Ready {
		t.Fatalf("not ready: %s", got.Reason)
	}
	if got.Reason != "" || len(got.Names) != 0 || len(got.Missing) != 0 || len(got.Fingerprint.Exists) != 0 {
		t.Fatalf("%+v", got)
	}
}

func requireKnowledgeReady(t *testing.T, got Knowledge, names ...string) {
	t.Helper()
	if !got.Ready {
		t.Fatalf("not ready: %s", got.Reason)
	}
	if got.Reason != "" {
		t.Fatalf("reason %q", got.Reason)
	}
	if len(got.Missing) != 0 {
		t.Fatalf("missing %+v", got.Missing)
	}
	if len(got.Names) != len(names) || len(got.Fingerprint.Exists) != len(names) {
		t.Fatalf("names %+v presence %+v", got.Names, got.Fingerprint.Exists)
	}
	for i, name := range names {
		if got.Names[i] != name || !got.Fingerprint.Exists[i] {
			t.Fatalf("names %+v presence %+v", got.Names, got.Fingerprint.Exists)
		}
	}
}

func requireKnowledgeReason(t *testing.T, got Knowledge, reason string) {
	t.Helper()
	if got.Ready {
		t.Fatal("ready")
	}
	if got.Reason != reason {
		t.Fatalf("reason %q want %q", got.Reason, reason)
	}
}

func requireNameFile(t *testing.T, got Knowledge, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("name file stat")
	}
	fp := got.Fingerprint
	if !fp.Present || fp.Inode != uint64(stat.Ino) || fp.Size != info.Size() || !fp.ModTime.Equal(info.ModTime()) {
		t.Fatalf("fingerprint inode %d size %d time %s, stat inode %d size %d time %s", fp.Inode, fp.Size, fp.ModTime, stat.Ino, info.Size(), info.ModTime())
	}
}
