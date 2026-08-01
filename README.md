# Hamid CNI — VPC-aware Kubernetes networking

Cluster CNI that models **VPCs as CRDs**. Namespaces may opt into a VPC via annotation;
otherwise they use the cluster **default VPC**. Pods get IPs from that VPC's CIDR.
Each VPC uses its own VXLAN VNI, so CIDRs may **overlap across VPCs** while remaining isolated.

## Architecture

```
┌─────────────┐     Unix socket      ┌──────────────────┐
│  kubelet /  │ ◄──────────────────► │  hamid-agent     │
│  hamid-cni  │                      │  (DaemonSet)     │
└─────────────┘                      │  • IPAM via CRDs │
                                     │  • bridge+VXLAN  │
                                     │  • FDB/neigh sync│
                                     └────────┬─────────┘
                                              │
                                     ┌────────▼─────────┐
                                     │  API server      │
                                     │  VPC / IPAlloc   │
                                     └────────┬─────────┘
                                              │
                                     ┌────────▼─────────┐
                                     │ hamid-controller │
                                     │ (validates VPCs) │
                                     └──────────────────┘
```

Per VPC on each node that hosts member pods:

- Linux bridge `hv-<vpc>`
- VXLAN device `vx-<vpc>` (VNI = `spec.vxlanID`, UDP 4789)
- Pod veth pairs enslaved to the bridge
- Static bridge FDB + ARP neigh entries for remote pods

## Quick start

```bash
# Build & load image (kind example)
make docker-build
kind load docker-image ghcr.io/hamid/hamid-cni:0.1.0

# Install (creates a default VPC automatically)
helm upgrade --install hamid-cni deploy/helm/hamid-cni \
  --namespace kube-system \
  --set image.tag=0.1.0

# Unannotated namespaces (e.g. kube-system) use VPC "default".
# Optional: dedicated VPCs (overlapping CIDRs are OK)
kubectl apply -f examples/vpcs.yaml
kubectl annotate namespace team-a network.hamid-cni.io/vpc=vpc-blue --overwrite
kubectl annotate namespace team-b network.hamid-cni.io/vpc=vpc-red --overwrite
```

## VPC selection

1. Namespace annotation `network.hamid-cni.io/vpc=<name>` if set  
2. Else the configured default VPC (Helm: `defaultVPC.name`, agent: `--default-vpc`, usually `default`)

## VPC CRD

```yaml
apiVersion: network.hamid-cni.io/v1alpha1
kind: VPC
metadata:
  name: vpc-blue
spec:
  cidr: 10.0.0.0/16
  vxlanID: 100          # must be unique cluster-wide
  gateway: 10.0.0.1     # optional
  excludeIPs: []        # optional
```

Namespace annotation (optional — omit to use the default VPC):

```yaml
metadata:
  annotations:
    network.hamid-cni.io/vpc: vpc-blue
```

## Components

| Binary | Role |
|--------|------|
| `hamid-cni` | CNI plugin installed to `/opt/cni/bin` |
| `hamid-agent` | Per-node datapath + IPAM |
| `hamid-controller` | VPC validation & status |

## Requirements

- Linux nodes with VXLAN support (`vxlan` kernel module)
- Cluster without a conflicting primary CNI (or configure as the sole conflist)
- Optional namespace annotation to select a non-default VPC; unannotated namespaces use `default`

## Development

```bash
make build          # binaries to bin/
make docker-build   # multi-binary image
make test
```

## License

Apache-2.0
