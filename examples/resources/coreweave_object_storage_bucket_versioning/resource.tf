resource "coreweave_object_storage_bucket" "default" {
  name = "bucket-versioning-example"
  zone = "US-EAST-04A"
}


resource "coreweave_object_storage_bucket_versioning" "default" {
  bucket = coreweave_object_storage_bucket.default.name
  versioning_configuration {
    status = "Enabled"
  }

}
