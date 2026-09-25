#!/usr/bin/env python3
"""Use PR 1361's generated protobufs without changing published dependencies."""

import argparse
from pathlib import Path
import shlex
import subprocess


PROTO_REVISION = "df6109d05f18e58c3d1003df4c2e78be05b1e174"
PROTO_MODULE = "buf.build/gen/go/coreweave/cks/protocolbuffers/go"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("cks_api", type=Path, help="Local cks-api repository")
    args = parser.parse_args()
    repository = args.cks_api.resolve()
    provider = Path(__file__).resolve().parents[1]
    source = "api/cks/coreweave/cks/v1beta1"
    files = ("clusters.pb.go", "public_access.pb.go")
    contents = {
        name: subprocess.check_output(
            ["git", "-C", str(repository), "show", f"{PROTO_REVISION}:{source}/{name}"]
        )
        for name in files
    }

    module = provider / "modules-dev" / "cks-pr1361"
    package = module / "coreweave" / "cks" / "v1beta1"
    package.mkdir(parents=True, exist_ok=True)
    # The prior draft generated this now-removed descriptor in the same module.
    (package / "protected_access.pb.go").unlink(missing_ok=True)
    for name, content in contents.items():
        (package / name).write_bytes(content)
    (module / "go.mod").write_text(f"module {PROTO_MODULE}\n\ngo 1.26.6\n")
    (module / "SOURCE").write_text(f"coreweave/cks-api PR 1361\n{PROTO_REVISION}\n")

    workspace = provider / "modules-dev" / "go.work"
    workspace.write_text(
        "go 1.26.6\n\nuse (\n\t..\n\t../tools\n)\n\n"
        f"replace {PROTO_MODULE} => ./cks-pr1361\n"
    )
    print(f"export GOWORK={shlex.quote(str(workspace))}")


if __name__ == "__main__":
    main()
