"""Check that branch probes preserve behavior and distinguish loop exits."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SOURCE = '''package main
import ("fmt"; "os")
func loop(values []int, stop bool) int {
    sum := 0
    for _, value := range values {
        if stop { break }
        sum += value
    }
    return sum
}
func early(values []int) bool {
    for _, value := range values {
        if value == 1 { return true }
    }
    return false
}
func nested() int {
    sum := 0
    for range []int{1,2} {
        for i:=0; i<2; i++ { break }
        sum++
    }
    return sum
}
func decision(value int) bool { return value > 0 }
func choose(value int) int {
    switch value { case 1,2: return 10; default: return 20 }
}
func noMatch(value int) {
    switch value { case 1: fmt.Print("one") }
}
func main() {
    switch os.Args[1] {
    case "break": fmt.Println(loop([]int{1,2},true))
    case "empty": fmt.Println(loop(nil,false))
    case "exhaust": fmt.Println(loop([]int{1,2},false))
    case "return": fmt.Println(early([]int{1}))
    case "return-false": fmt.Println(early([]int{2}))
    case "nested": fmt.Println(nested())
    case "decisions": fmt.Println(decision(1),decision(0),choose(1),choose(3)); noMatch(0)
    }
}
'''


class InstrumentationTests(unittest.TestCase):
    def test_behavior_and_decision_edges(self):
        with tempfile.TemporaryDirectory(prefix="branch-audit-test-") as raw:
            root = Path(raw)
            source = root / "source"
            source.mkdir()
            (source / "go.mod").write_text("module branchfixture\n\ngo 1.25.0\n")
            (source / "main.go").write_text(SOURCE)
            (source / "main_test.go").write_text('''package main
import "testing"
func TestDecision(t *testing.T) {
    if !decision(1) { t.Fatal("positive") }
    t.Run("zero",func(t *testing.T) {
        if decision(0) { t.Fatal("zero") }
    })
}
''')
            original = root / "original"
            subprocess.run(["go", "build", "-o", str(original), "."], cwd=source, check=True)
            manifest = root / "manifest.json"
            subprocess.run(["go", "run", str(ROOT / "scripts/branch-instrument.go"),
                            str(source), str(manifest)], cwd=ROOT, check=True)
            binary = root / "instrumented"
            subprocess.run(["go", "build", "-o", str(binary), "."], cwd=source, check=True)
            branches = json.loads(manifest.read_text())
            ranges = {branch["line"]: branch["id"] for branch in branches if branch["kind"] == "range"}
            loop_id, early_id, nested_id = (ranges[line] for line in (5, 12, 19))
            all_hits = set()
            for mode in ("break", "empty", "exhaust", "return", "return-false", "nested", "decisions"):
                with self.subTest(mode=mode):
                    hits = root / mode
                    hits.mkdir()
                    expected = subprocess.check_output([str(original), mode])
                    actual = subprocess.check_output([str(binary), mode], env=os.environ | {
                        "STOCK_BRANCH_COVERDIR": str(hits), "STOCK_BRANCH_TEST": mode})
                    self.assertEqual(actual, expected)
                    records = [json.loads(path.read_text()) for path in hits.iterdir()]
                    self.assertTrue(all(record["test"] == mode for record in records))
                    visited = {record["id"] for record in records}
                    all_hits |= visited
                    if mode == "break":
                        self.assertIn(loop_id + ":true", visited)
                        self.assertNotIn(loop_id + ":false", visited)
                    elif mode == "empty":
                        self.assertNotIn(loop_id + ":true", visited)
                        self.assertIn(loop_id + ":false", visited)
                    elif mode == "exhaust":
                        self.assertTrue({loop_id + ":true", loop_id + ":false"} <= visited)
                    elif mode == "return":
                        self.assertNotIn(early_id + ":false", visited)
                    elif mode == "return-false":
                        self.assertIn(early_id + ":false", visited)
                    elif mode == "nested":
                        self.assertIn(nested_id + ":false", visited)
            return_branch = next(branch for branch in branches if branch["kind"] == "return")
            self.assertTrue({return_branch["id"] + ":true", return_branch["id"] + ":false"} <= all_hits)
            no_match = next(branch for branch in branches if branch["line"] == 30 and branch["kind"] == "switch-default")
            self.assertIn(no_match["id"] + ":taken", all_hits)
            test_hits = root / "tests"
            test_hits.mkdir()
            subprocess.run(["go", "test", "./..."], cwd=source, check=True, env=os.environ | {
                "STOCK_BRANCH_COVERDIR": str(test_hits)})
            records = [json.loads(path.read_text()) for path in test_hits.iterdir()]
            self.assertIn({"id": return_branch["id"] + ":true", "test": "TestDecision"}, records)
            self.assertIn({"id": return_branch["id"] + ":false", "test": "TestDecision/zero"}, records)

    def test_unsupported_construct_fails_explicitly(self):
        with tempfile.TemporaryDirectory(prefix="branch-audit-test-") as raw:
            root = Path(raw)
            (root / "main.go").write_text("package main\nfunc main(){var v any; switch v.(type){case int:}}\n")
            result = subprocess.run(["go", "run", str(ROOT / "scripts/branch-instrument.go"),
                                     str(root), str(root / "manifest.json")], cwd=ROOT,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(b"need explicit instrumentation", result.stderr)


if __name__ == "__main__":
    unittest.main()
