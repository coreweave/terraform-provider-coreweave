resource "coreweave_object_storage_bucket" "default" {
  name = "my-tf-test-bucket"
  zone = "US-EAST-04A"
  tags = {
    "foo" = bar
  }
}
