---
title: go-oci-blob
slug: /
description: Go library for uploading and downloading OCI blobs.
---

# go-oci-blob

go-oci-blob is a Go library that uploads and downloads OCI blobs. It covers
the blob subset of the OCI distribution spec and nothing else: push, pull,
existence checks, and cross-repository mounts, with retries and digest
verification built in.

The library is pre-release. The [design](explanation/design.md) page records
the architecture and API direction.
