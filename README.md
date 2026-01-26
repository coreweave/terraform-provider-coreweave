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

See the [CoreWeave Provider documentation](https://registry.terraform.io/providers/coreweave/coreweave/latest/docs) to get started using the CoreWeave provider.

## License

MIT Licensed. See [LICENSE](https://github.com/coreweave/terraform-provider-coreweave/tree/main/LICENSE) for full details.
