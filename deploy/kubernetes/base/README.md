# Kubernetes Base

This directory contains a minimal Kustomize base for deploying `agent-http`.

## Usage

Create a real secret from the example before applying the base:

```sh
kubectl apply -f secret.example.yaml
kubectl apply -k .
```

The base intentionally does not include database, Redis, or object-store manifests. Production clusters should use managed services or platform-approved charts for PostgreSQL/pgvector, Redis, and S3-compatible storage.

## Worker Template

`worker-deployment.example.yaml` is not included in `kustomization.yaml` because workers are application-specific: the host service owns driver imports, scenario wiring, queue selection, and tool executors. Use the file as a shape for the worker process built by your application.