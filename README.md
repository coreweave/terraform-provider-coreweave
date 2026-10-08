Copyright (c) 2024-2025 CoreWeave, Inc.

# CoreWeave Terraform Provider

- Documentation: https://registry.terraform.io/providers/coreweave/coreweave/latest/docs

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://golang.org/doc/install) >= 1.25

## Development

### Mise

It is recommended to use [mise](https://mise.jdx.dev/) to manage tool versions 
associated with local development. After you have [mise installed](https://mise.jdx.dev/installing-mise.html),
just run:

```shell
mise install
```

to install the relevant tools needed for building. After that:

```shell
make build
```

### Dev Builds

To run the provider with a dev build, invoke the script `./devtf` to invoke your local terraform binary against a dev build from the current working copy, passing terraform env and args as normal. For example:

```bash
./devtf apply -compact-warnings -var a=1
```

### Debugging

Debugging the provider can be a bit complicated. To do so, we must run the provider itself _as a server_, and then configure our terraform CLI to use the provider server, instead of invoking it directly. The debugger will then step through the code, as it's invoked by the terraform CLI. This means that the terraform process will continue running (and waiting for the provider to finish its work, even if it's waiting on a breakpoint), while we operate. The terraform CLI and the provider's processes are fully decoupled in this mode. Keep this in mind when using it.

#### Debugging Setup

You will need to install delve (`dlv`) or use the built-in VSCode delve version.

You must create a debug env file, `touch __debug.env`. Because the provider server runs as a process separate from terraform itself, it is unable to inherit environment variables from the terraform process. This is useful for injecting environment variables to the provider, to configure credentials or provider settings.

#### Debugging via VS Code Debugger

Invoke the debugger by selecting "Debug Terraform Provider Server" in the "run and debug" menu. The debug console will open, and include a line that starts with `TF_REATTACH_PROVIDERS`. Copy this line into your shell, and `export` it, like `TF_REATTACH_PROVIDERS=...`. `terraform` calls from this shell will now use the delve session.

#### Debugging Manually

The following is a good starting point to run the debugger:

```bash
make debug
```

The output will include a line that starts with `TF_REATTACH_PROVIDERS`. Copy this line into the shell you wish to run terraform from, and `export` it, like `TF_REATTACH_PROVIDERS=...`. `terraform` calls from this shell will now use the delve session.

Note: When finished, you may need to `kill` `dlv`'s PID.

### Building The Provider

Clone repository to: `$GOPATH/src/github.com/coreweave/terraform-provider-coreweave`

```sh
$ mkdir -p $GOPATH/src/github.com/coreweave; cd $GOPATH/src/github.com/coreweave
$ git clone git@github.com:coreweave/terraform-provider-coreweave
```

Enter the provider directory and build the provider

```sh
$ cd $GOPATH/src/github.com/coreweave/terraform-provider-coreweave
$ make build
```

## Using the provider

### HotLoad acceptance tests

`TestInferenceHotLoadAcceptance` uses real Terraform against staging and consumes
one GPU. It creates its own gateway and deployment, checks import and immutable
replacement plans, completes a full update followed by a delta, and verifies
refresh, no-op plans, operation import and retained history after terminal destroy.
Harness cleanup targets only resources created by this test. No existing
deployment IDs are accepted. Default CI skips this test.

Before opting in, publish three valid, compatible checkpoints under a fresh,
exclusive prefix: initial full, replacement full and an XOR delta based on the
replacement full. Their metadata identities must be globally unique for this
run, with the delta base matching the replacement full identity. The initial
model path is `<prefix>/<initial-identity>`. Never use missing checkpoints as
cancellation fixtures: they can quarantine a worker.

Set `COREWEAVE_API_ENDPOINT=https://api.staging.coreweave.com` and
`COREWEAVE_API_TOKEN` for a HotLoad-enabled staging organization, such as sa32a4.
Also set `INFR_ZONE`, `INFR_INSTANCE_ID` (for example `gd-1xgh200`),
`INFR_HOTLOAD_RUNTIME_VERSION` (a supported dynamo-vllm version),
`INFR_HOTLOAD_BUCKET`, `INFR_HOTLOAD_PREFIX`, `INFR_HOTLOAD_INITIAL_IDENTITY`,
`INFR_HOTLOAD_FULL_IDENTITY`, and `INFR_HOTLOAD_DELTA_IDENTITY`.

```bash
TF_ACC=1 INFR_HOTLOAD_ACCEPTANCE=1 \
  go test ./coreweave/inference \
  -run '^TestInferenceHotLoadAcceptance$' -count=1 -timeout=90m -v
```

Live cancellation is not exercised while staging returns `UNIMPLEMENTED`.
Mock tests cover active cancellation, terminal destroy and state retention on
cancellation errors, including `UNIMPLEMENTED`. If a live run fails with an
active operation, cancellation may prevent automatic cleanup; inspect the
reported test resource IDs instead of sweeping unrelated deployments.

See the [CoreWeave Provider documentation](https://registry.terraform.io/providers/coreweave/coreweave/latest/docs) to get started using the CoreWeave provider.

## License

MIT Licensed. See [LICENSE](https://github.com/coreweave/terraform-provider-coreweave/tree/main/LICENSE) for full details.
