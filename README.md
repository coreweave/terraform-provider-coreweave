Copyright (c) 2024-2-25 CoreWeave, Inc.

# CoreWeave Terraform Provider

- Documentation: https://registry.terraform.io/providers/coreweave/coreweave/latest/docs

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://golang.org/doc/install) >= 1.22

## Building The Provider

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
