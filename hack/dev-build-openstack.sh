#!/usr/bin/env bash

# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.


###############################################################################
#
# dev-build.sh
#
# Convenience script for developing kops AND nodeup.
#
# This script (by design) will handle building a full kops cluster in AWS,
# with a custom version of the nodeup, protokube and dnscontroller.
#
# This script and Makefile uses aws client
# https://aws.amazon.com/cli/
# and make sure you `aws configure`
#
# # Example usage
#
# KOPS_STATE_STORE="s3://my-dev-s3-state" \
# CLUSTER_NAME="fullcluster.name.mydomain.io" \
# NODEUP_BUCKET="s3-devel-bucket-name-store-nodeup" \
# IMAGE="kope.io/k8s-1.6-debian-jessie-amd64-hvm-ebs-2017-05-02" \
# ./dev-build.sh
# 
# # TLDR;
# 1. setup dns in route53
# 2. create s3 buckets - state store and nodeup bucket
# 3. set zones appropriately, you need 3 zones in a region for HA
# 4. run script
# 5. find bastion to ssh into (look in ELBs)
# 6. use ssh-agent and ssh -A
# 7. your pem will be the access token
# 8. user is admin, and the default is debian
# 
# # For more details see:
#
# https://github.com/kubernetes/kops/blob/master/docs/aws.md
#
###############################################################################

KOPS_DIRECTORY="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

## Set custom variables
KOPS_STATE_STORE="swift://raviku7-kops-test"
IMAGE="UBUNTU-16.04-CORE"
CLUSTER_NAME="raviku7-kops-test.k8s.local"
NODEUP_BUCKET="s3://empty"
OS_DOMAIN_NAME=$OS_PROJECT_DOMAIN_NAME
KOPS_FEATURE_FLAGS=AlphaAllowOpenstack
KOPS_BINARY=./.build/dist/linux/amd64/kops

## For local debugging:
# export  NODEUP_URL=http://64.102.180.37/dist/nodeup KOPS_BASE_URL=http://64.102.180.37/dist KOPS_FEATURE_FLAGS=AlphaAllowOpenstack OS_DOMAIN_NAME=$OS_PROJECT_DOMAIN_NAME

#
# Check that required binaries are installed
#
command -v make >/dev/null 2>&1 || { echo >&2 "I require make but it's not installed.  Aborting."; exit 1; }
command -v go >/dev/null 2>&1 || { echo >&2 "I require go but it's not installed.  Aborting."; exit 1; }
command -v docker >/dev/null 2>&1 || { echo >&2 "I require docker but it's not installed.  Aborting."; exit 1; }
command -v aws >/dev/null 2>&1 || { echo >&2 "I require aws cli but it's not installed.  Aborting."; exit 1; }

#
# Check that expected vars are set
#
[ -z "$KOPS_STATE_STORE" ] && echo "Need to set KOPS_STATE_STORE" && exit 1;
[ -z "$CLUSTER_NAME" ] && echo "Need to set CLUSTER_NAME" && exit 1;
[ -z "$NODEUP_BUCKET" ] && echo "Need to set NODEUP_BUCKET" && exit 1;
[ -z "$IMAGE" ] && echo "Need to set IMAGE or use the image listed here https://github.com/kubernetes/kops/blob/master/channels/stable" && exit 1;

# Cluster config
NODE_COUNT=${NODE_COUNT:-3}
NODE_ZONES=${NODE_ZONES:-"rcdn-1-a,rcdn-1-b,rcdn-1-c"}
NODE_SIZE=${NODE_SIZE:-8vCPUx32GB}
MASTER_ZONES=${MASTER_ZONES:-"rcdn-1-a"}
MASTER_SIZE=${MASTER_SIZE:-4vCPUx16GB}
KOPS_CREATE=${KOPS_CREATE:-yes}

# NETWORK
TOPOLOGY=${TOPOLOGY:-private}
NETWORKING=${NETWORKING:-flannel}

# How verbose go logging is
VERBOSITY=${VERBOSITY:-10}

cd $KOPS_DIRECTORY/..

GIT_VER=git-$(git describe --always)
[ -z "$GIT_VER" ] && echo "we do not have GIT_VER something is very wrong" && exit 1;

echo ==========
echo "Starting build"

# removing CI=1 because it forces a new upload every time
# export CI=1
if [[ $1 == 'rebuild' ]]; then
  make && UPLOAD_DEST=s3://${NODEUP_BUCKET} make upload

  echo ==========
  echo "Uploading files to temp server..."
  rsync -avz --exclude .build/darwin --exclude .build/windows --exclude .build/local --exclude .build/artifacts .build bastion-compute-sdp6:/home/centos/raviku7-kops-test/

fi

# removing make test since it relies on the files in the bucket
# && make test

KOPS_VERSION=$(./.build/dist/linux/amd64/kops version --short)
KOPS_BASE_URL="https://s3.us-east-2.amazonaws.com/public-files.pz9tbevxyiqglv9e/kops-dev/kops/1.12.0-alpha.1"
NODEUP_URL="${KOPS_BASE_URL}/linux/amd64/nodeup"

echo "KOPS_BASE_URL=${KOPS_BASE_URL}"
echo "NODEUP_URL=${NODEUP_URL}"

echo ==========
echo "Deleting cluster ${CLUSTER_NAME}. Elle est finie."

${KOPS_BINARY} delete cluster \
  --name $CLUSTER_NAME \
  --state $KOPS_STATE_STORE \
  -v $VERBOSITY \
  --yes

echo ==========
echo "Creating cluster ${CLUSTER_NAME}"

kops_command="\
NODEUP_URL=${NODEUP_URL} \
KOPS_BASE_URL=${KOPS_BASE_URL} \
KOPS_FEATURE_FLAGS=AlphaAllowOpenstack \
${KOPS_BINARY} create cluster \
  --name $CLUSTER_NAME \
  --state $KOPS_STATE_STORE \
  --node-count $NODE_COUNT \
  --zones nova \
  --network-cidr 10.0.0.0/24 \
  --kubernetes-version 1.11.7 \
  --master-count 1 \
  --etcd-storage-type CBS \
  --api-loadbalancer-type public \
  --cloud openstack \
  --master-zones $MASTER_ZONES \
  --node-size $NODE_SIZE \
  --master-size $MASTER_SIZE \
  -v $VERBOSITY \
  --image $IMAGE \
  --channel alpha \
  --topology $TOPOLOGY \
  --networking $NETWORKING \
  --bastion \
  --os-ext-net tenant-internal-floatingip-net \
  --ssh-public-key ~/.ssh/p3/raviku7-kops-test.pub \
  "

if [[ $TOPOLOGY == "private" ]]; then
  kops_command+=" --bastion='true'"
fi

if [ -n "${KOPS_FEATURE_FLAGS+x}" ]; then 
  kops_command=KOPS_FEATURE_FLAGS="${KOPS_FEATURE_FLAGS}" $kops_command
fi

if [[ $KOPS_CREATE == "yes" ]]; then 
  kops_command="$kops_command --yes"
fi

echo "EXECUTE: $kops_command"

eval $kops_command

echo ==========
echo "Your k8s cluster ${CLUSTER_NAME}, awaits your bidding."
