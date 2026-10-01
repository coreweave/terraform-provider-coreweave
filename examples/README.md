# Examples

This directory contains examples that are mostly used for documentation, but can also be run/tested manually via the Terraform CLI.

The document generation tool looks for files in the following locations by default. All other *.tf files besides the ones mentioned below are ignored by the documentation tool. This is useful for creating examples that can run and/or are testable even if some parts are not relevant for the documentation.

* **provider/provider.tf** example file for the provider index page
* **data-sources/`full data source name`/data-source.tf** example file for the named data source page
* **resources/`full resource name`/resource.tf** example file for the named data source page

## Validating documentation examples

Independently copyable examples should declare `coreweave/coreweave` in a `terraform.required_providers` block so Terraform resolves the correct provider when a user copies the example into a new root module.

Run the credential-free validation harness from the repository root:

```bash
make -f GNUmakefile test-examples
```

The harness copies each registered example into a temporary directory, then runs `terraform fmt -check`, `terraform init -backend=false`, and `terraform validate`. It does not run `terraform plan` or `apply`, and it does not require CoreWeave API credentials.

To register a new example, add its directory path to the `example_dirs` list in [tools/test-examples.sh](../tools/test-examples.sh).
