#!/usr/bin/env python3
"""Measure production statements and decision edges, including CLI subprocesses."""
import collections
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MODULE = "github.com/hisho-123/stock_notification_backend/"


def run(*args, cwd=ROOT, env=None):
    subprocess.run(args, cwd=cwd, env=env, check=True)


def main():
    if not os.environ.get("TEST_DATABASE_URL"):
        raise SystemExit("TEST_DATABASE_URL is required; use a dedicated test database")
    output = ROOT / "coverage"
    output.mkdir(exist_ok=True)
    env = os.environ.copy()
    with tempfile.TemporaryDirectory(prefix="stock-coverage-") as raw:
        work = Path(raw)
        cli = work / "cli"
        env["STOCK_CLI_COVERDIR"] = str(cli)
        unit_profile = work / "tests.cover"
        run("go", "test", "-race", "-count=1", "-tags=integration", "-covermode=atomic",
            "-coverpkg=./...", f"-coverprofile={unit_profile}", "./...", env=env)
        cli_profile = work / "cli.cover"
        run("go", "tool", "covdata", "textfmt", f"-i={cli}", f"-o={cli_profile}")
        blocks = {}
        for profile in (unit_profile, cli_profile):
            for line in profile.read_text().splitlines()[1:]:
                location, statements, count = line.rsplit(" ", 2)
                old = blocks.get(location, (int(statements), 0))
                if old[0] != int(statements):
                    raise SystemExit(f"inconsistent coverage block: {location}")
                blocks[location] = int(statements), old[1] + int(count)
        combined = output / "statements.cover"
        combined.write_text("mode: atomic\n" + "".join(
            f"{location} {n} {count}\n" for location, (n, count) in sorted(blocks.items())))
        totals = collections.defaultdict(lambda: [0, 0])
        missing_statements = []
        for location, (n, count) in blocks.items():
            filename = location.split(":", 1)[0].removeprefix(MODULE)
            package = str(Path(filename).parent)
            totals[package][0] += n
            totals[package][1] += n if count else 0
            if n and not count:
                missing_statements.append(location.removeprefix(MODULE))

        # Instrument only a disposable copy. No source files in ROOT are rewritten.
        copy = work / "source"
        copy.mkdir()
        for filename in ("go.mod", "go.sum"):
            shutil.copy2(ROOT / filename, copy / filename)
        for directory in ("cmd", "internal", "tests", "migrations", "web"):
            shutil.copytree(ROOT / directory, copy / directory)
        manifest = work / "branches.json"
        run("go", "run", str(ROOT / "scripts/branch-instrument.go"), str(copy), str(manifest))
        hits = work / "branches"
        hits.mkdir()
        audit_env = env | {"STOCK_BRANCH_COVERDIR": str(hits),
                           "STOCK_CLI_COVERDIR": str(work / "audit-cli")}
        run("go", "test", "-race", "-count=1", "-tags=integration", "./...", cwd=copy, env=audit_env)
        tests_by_edge = collections.defaultdict(set)
        for path in hits.iterdir():
            hit = json.loads(path.read_text())
            tests_by_edge[hit["id"]].add(hit["test"])
        branches = json.loads(manifest.read_text())
        missing_edges = []
        branch_totals = collections.defaultdict(lambda: [0, 0])
        for branch in branches:
            branch["covered"] = []
            branch["tests"] = {}
            for edge in branch["edges"]:
                branch_totals[str(Path(branch["file"]).parent)][0] += 1
                tests = sorted(tests_by_edge[branch["id"] + ":" + edge])
                branch["tests"][edge] = tests
                if tests:
                    branch["covered"].append(edge)
                    branch_totals[str(Path(branch["file"]).parent)][1] += 1
                else:
                    missing_edges.append(branch["id"] + ":" + edge)
        (output / "branches.json").write_text(json.dumps(branches, ensure_ascii=False, indent=2) + "\n")
        correspondence = ["# C1の分岐とテストの対応表", "",
                          "`make coverage`で生成。パス・行番号は元の本番コードに対応する。", "",
                          "| 判定位置 | 種類 | 分岐 | 通過したテスト／サブテスト |",
                          "| --- | --- | --- | --- |"]
        for branch in branches:
            for edge in branch["edges"]:
                tests = ", ".join(f"`{name}`" for name in branch["tests"][edge]) or "未到達"
                correspondence.append(f"| {branch['file']}:{branch['line']} | {branch['kind']} | {edge} | {tests} |")
        (output / "branches.md").write_text("\n".join(correspondence) + "\n")
        print("\nPackage | statements covered/total | decision edges covered/total")
        for package, (total, covered) in sorted(totals.items()):
            edge_total, edge_covered = branch_totals[package]
            print(f"{package}: C0 {covered}/{total}; C1 {edge_covered}/{edge_total}")
        print(f"Total C0: {sum(v[1] for v in totals.values())}/{sum(v[0] for v in totals.values())}")
        print(f"Total C1: {sum(v[1] for v in branch_totals.values())}/{sum(v[0] for v in branch_totals.values())}")
        if missing_statements or missing_edges:
            for label, missing in (("Uncovered statements", missing_statements),
                                   ("Uncovered decision edges", missing_edges)):
                print(label + ":")
                for item in sorted(missing):
                    print("  " + item)
            raise SystemExit(1)


if __name__ == "__main__":
    main()
