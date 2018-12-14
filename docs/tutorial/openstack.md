# Getting Started with kops on OpenStack

**WARNING**: OpenStack support on kops is currently **alpha** meaning it is in the early stages of development and subject to change, please use with caution.

## Obtain and source the openstack RC file
The Cloud Config used by the kubernetes API server and kubelet will be constructed from environment variables in the openstack RC file.
```bash
source openstack.rc
```


## Environment Variables

It is important to set the following environment variables:
```bash
export KOPS_STATE_STORE=swift://<bucket-name> # where <bucket-name> is the name of the Swift container to use for kops state

# this is required since OpenStack support is currently in alpha so it is feature gated
export KOPS_FEATURE_FLAGS="AlphaAllowOpenStack"
```

## Creating a Cluster

```bash
# the etcd storage type can be retrieved by
openstack volume type list
# coreos (the default) + flannel overlay cluster in Default
kops create cluster --cloud=openstack --etcd-storage-type=CBS --name=my-cluster.k8s.local --networking=flannel --zones=Default --network-cidr=192.168.0.0/16
# to update a cluster
kops update cluster my-cluster.k8s.local --yes

# to delete a cluster
# Not implemented yet...
# kops delete cluster my-cluster.k8s.local --yes
```

## Features Still in Development

kops for OpenStack currently does not support these features:
* cluster delete

