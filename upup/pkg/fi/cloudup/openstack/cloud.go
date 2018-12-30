/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package openstack

import (
	"crypto/tls"
	"fmt"
	"github.com/drekle/kops/pkg/dns"
	"net/http"
	"time"

	"github.com/golang/glog"
	"github.com/gophercloud/gophercloud"
	os "github.com/gophercloud/gophercloud/openstack"
	cinder "github.com/gophercloud/gophercloud/openstack/blockstorage/v2/volumes"
	az "github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/availabilityzones"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/floatingips"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/keypairs"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/servergroups"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/volumeattach"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/dns/v2/recordsets"
	"github.com/gophercloud/gophercloud/openstack/dns/v2/zones"
	"github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/listeners"
	"github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/loadbalancers"
	v2pools "github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/pools"
	l3floatingip "github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/layer3/routers"
	sg "github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/security/groups"
	sgr "github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/security/rules"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/kops/dnsprovider/pkg/dnsprovider"
	"k8s.io/kops/dnsprovider/pkg/dnsprovider/providers/openstack/designate"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/cloudinstances"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/util/pkg/vfs"
)

const TagNameEtcdClusterPrefix = "k8s.io/etcd/"
const TagNameRolePrefix = "k8s.io/role/"
const TagClusterName = "KubernetesCluster"

// ErrNotFound is used to inform that the object is not found
var ErrNotFound = "Resource not found"

// readBackoff is the backoff strategy for openstack read retries.
var readBackoff = wait.Backoff{
	Duration: time.Second,
	Factor:   1.5,
	Jitter:   0.1,
	Steps:    4,
}

// writeBackoff is the backoff strategy for openstack write retries.
var writeBackoff = wait.Backoff{
	Duration: time.Second,
	Factor:   1.5,
	Jitter:   0.1,
	Steps:    5,
}

type OpenstackCloud interface {
	fi.Cloud

	ComputeClient() *gophercloud.ServiceClient
	BlockStorageClient() *gophercloud.ServiceClient
	NetworkingClient() *gophercloud.ServiceClient
	LoadBalancerClient() *gophercloud.ServiceClient
	DNSClient() *gophercloud.ServiceClient

	// GetApiIngressStatus returns locations used to connect to the api server externally
	GetApiIngressStatus(cluster *kops.Cluster) ([]kops.ApiIngressStatus, error)

	// GetCloudTags will return the tags attached on cloud
	GetCloudTags() map[string]string

	// Region returns the region which cloud will run on
	Region() string

	// ListVolumes will return the Cinder volumes which match the options
	ListVolumes(opt cinder.ListOptsBuilder) ([]cinder.Volume, error)

	// CreateVolume will create a new Cinder Volume
	CreateVolume(opt cinder.CreateOptsBuilder) (*cinder.Volume, error)

	// AttachVolume attaches the volume to a server, provide a server ID and attach options
	AttachVolume(serverID string, opt volumeattach.CreateOpts) (*volumeattach.VolumeAttachment, error)

	// SetVolumeTags will set the tags for the Cinder volume
	SetVolumeTags(id string, tags map[string]string) error

	//ListSecurityGroups will return the Neutron security groups which match the options
	ListSecurityGroups(opt sg.ListOpts) ([]sg.SecGroup, error)

	//CreateSecurityGroup will create a new Neutron security group
	CreateSecurityGroup(opt sg.CreateOptsBuilder) (*sg.SecGroup, error)

	//ListSecurityGroupRules will return the Neutron security group rules which match the options
	ListSecurityGroupRules(opt sgr.ListOpts) ([]sgr.SecGroupRule, error)

	//CreateSecurityGroupRule will create a new Neutron security group rule
	CreateSecurityGroupRule(opt sgr.CreateOptsBuilder) (*sgr.SecGroupRule, error)

	//GetNetwork will return the Neutron network which match the id
	GetNetwork(networkID string) (*networks.Network, error)

	//ListNetworks will return the Neutron networks which match the options
	ListNetworks(opt networks.ListOptsBuilder) ([]networks.Network, error)

	//ListExternalNetworks will return the Neutron networks with the router:external property
	GetExternalNetwork() (*networks.Network, error)

	//CreateNetwork will create a new Neutron network
	CreateNetwork(opt networks.CreateOptsBuilder) (*networks.Network, error)

	//ListRouters will return the Neutron routers which match the options
	ListRouters(opt routers.ListOpts) ([]routers.Router, error)

	// availability_zone.go
	//
	// Returns the availability zones for the service client passed (compute, volume, network)
	ListAvailabilityZones(serviceClient *gophercloud.ServiceClient) ([]az.AvailabilityZone, error)
	// Returns the most appropriate storage availability zone given a compute availability zone
	GetStorageAZFromCompute(azName string) (*az.AvailabilityZone, error)

	// dns.go
	//
	// ListDNSZones will list available DNS zones
	ListDNSZones(opt zones.ListOptsBuilder) ([]zones.Zone, error)
	// ListDNSRecordsets will list the DNS recordsets for the given zone id
	ListDNSRecordsets(zoneID string, opt recordsets.ListOptsBuilder) ([]recordsets.RecordSet, error)

	// floatingip.go
	//
	// GetFloatingIP returns a floatingip given its ID
	GetFloatingIP(id string) (fip *floatingips.FloatingIP, err error)
	// AssociateFloatingIPToInstance will associate a floating ip to a server provided a Server ID
	AssociateFloatingIPToInstance(serverID string, opts floatingips.AssociateOpts) (err error)
	// ListFloatingIPs will list all available floating IPs
	ListFloatingIPs() (fips []floatingips.FloatingIP, err error)
	// ListL3FloatingIPs will list all available layer 3 floating IPs given the layer3 extension list options
	ListL3FloatingIPs(opts l3floatingip.ListOpts) (fips []l3floatingip.FloatingIP, err error)
	// CreateFloatingIP will create a floating IP
	CreateFloatingIP(opts floatingips.CreateOpts) (*floatingips.FloatingIP, error)
	// CreateL3FloatingIP will create a L3 floating IP
	CreateL3FloatingIP(opts l3floatingip.CreateOpts) (fip *l3floatingip.FloatingIP, err error)

	// instance.go
	//
	// ListInstances will list openstack servers
	ListInstances(servers.ListOptsBuilder) ([]servers.Server, error)
	// CreateInstance will create an openstack server
	CreateInstance(servers.CreateOptsBuilder) (*servers.Server, error)
	// Delete instance will delete an openstack server *NOT IMPLEMENTED*
	// DeleteInstance(i *cloudinstances.CloudInstanceGroupMember)

	// keypair.go
	//
	// ListKeypair will return the Nova keypairs
	ListKeypair(name string) (*keypairs.KeyPair, error)
	// CreateKeypair will create a new Nova Keypair
	CreateKeypair(opt keypairs.CreateOptsBuilder) (*keypairs.KeyPair, error)

	// loadbalancer.go
	//
	// GetLB retrieves a loadbalancer from its ID
	GetLB(loadbalancerID string) (*loadbalancers.LoadBalancer, error)
	// CreateLB will create an openstack loadbalancer
	CreateLB(opt loadbalancers.CreateOptsBuilder) (*loadbalancers.LoadBalancer, error)
	// ListLBs will list openstack loadbalancers
	ListLBs(opt loadbalancers.ListOptsBuilder) ([]loadbalancers.LoadBalancer, error)
	// AssociateToPool will associate a server to a pool given the pools ID
	AssociateToPool(server *servers.Server, poolID string, opts v2pools.CreateMemberOpts) (*v2pools.Member, error)
	// CreatePool will create an openstack pool
	CreatePool(opts v2pools.CreateOpts) (*v2pools.Pool, error)
	// ListPools will list openstack pools
	ListPools(v2pools.ListOpts) ([]v2pools.Pool, error)
	// ListListeners will list openstack listeners
	ListListeners(opts listeners.ListOpts) ([]listeners.Listener, error)
	// CreateListener will create an openstack listener
	CreateListener(opts listeners.CreateOpts) (*listeners.Listener, error)

	// network.go
	//
	// GetNetwork will return the Neutron network which match the id
	GetNetwork(networkID string) (*networks.Network, error)
	// ListNetworks will return the Neutron networks which match the options
	ListNetworks(opt networks.ListOptsBuilder) ([]networks.Network, error)
	// ListExternalNetworks will return the Neutron networks with the router:external property
	GetExternalNetwork() (*networks.Network, error)
	// CreateNetwork will create a new Neutron network
	CreateNetwork(opt networks.CreateOptsBuilder) (*networks.Network, error)

	// port.go
	//
	//ListPorts will create a Neutron ports which match the options
	CreatePort(opt ports.CreateOptsBuilder) (*ports.Port, error)
	//ListPorts will return the Neutron ports which match the options
	ListPorts(opt ports.ListOptsBuilder) ([]ports.Port, error)

	// router.go
	//
	//ListRouters will return the Neutron routers which match the options
	ListRouters(opt routers.ListOpts) ([]routers.Router, error)
	//CreateRouter will create a new Neutron router
	CreateRouter(opt routers.CreateOptsBuilder) (*routers.Router, error)
	//CreateRouterInterface will create a new Neutron router interface
	CreateRouterInterface(routerID string, opt routers.AddInterfaceOptsBuilder) (*routers.InterfaceInfo, error)

	// security_group.go
	//
	//ListSecurityGroups will return the Neutron security groups which match the options
	ListSecurityGroups(opt sg.ListOpts) ([]sg.SecGroup, error)
	//CreateSecurityGroup will create a new Neutron security group
	CreateSecurityGroup(opt sg.CreateOptsBuilder) (*sg.SecGroup, error)
	//ListSecurityGroupRules will return the Neutron security group rules which match the options
	ListSecurityGroupRules(opt sgr.ListOpts) ([]sgr.SecGroupRule, error)
	//CreateSecurityGroupRule will create a new Neutron security group rule
	CreateSecurityGroupRule(opt sgr.CreateOptsBuilder) (*sgr.SecGroupRule, error)

	// server_group.go
	//
	// CreateServerGroup will create a new server group.
	CreateServerGroup(opt servergroups.CreateOptsBuilder) (*servergroups.ServerGroup, error)
	// ListServerGroups will list available server groups
	ListServerGroups() ([]servergroups.ServerGroup, error)

	// subnet.go
	//
	//ListSubnets will return the Neutron subnets which match the options
	ListSubnets(opt subnets.ListOptsBuilder) ([]subnets.Subnet, error)
	//CreateSubnet will create a new Neutron subnet
	CreateSubnet(opt subnets.CreateOptsBuilder) (*subnets.Subnet, error)

	GetLB(loadbalancerID string) (*loadbalancers.LoadBalancer, error)

	CreateLB(opt loadbalancers.CreateOptsBuilder) (*loadbalancers.LoadBalancer, error)

	ListLBs(opt loadbalancers.ListOptsBuilder) ([]loadbalancers.LoadBalancer, error)

	GetApiIngressStatus(cluster *kops.Cluster) ([]kops.ApiIngressStatus, error)

	// DefaultInstanceType determines a suitable instance type for the specified instance group
	DefaultInstanceType(cluster *kops.Cluster, ig *kops.InstanceGroup) (string, error)

	// Returns the availability zones for the service client passed (compute, volume, network)
	ListAvailabilityZones(serviceClient *gophercloud.ServiceClient) ([]az.AvailabilityZone, error)

	AssociateToPool(server *servers.Server, poolID string, opts v2pools.CreateMemberOpts) (*v2pools.Member, error)

	CreatePool(opts v2pools.CreateOpts) (*v2pools.Pool, error)

	ListPools(v2pools.ListOpts) ([]v2pools.Pool, error)

	ListListeners(opts listeners.ListOpts) ([]listeners.Listener, error)

	CreateListener(opts listeners.CreateOpts) (*listeners.Listener, error)

	GetStorageAZFromCompute(azName string) (*az.AvailabilityZone, error)

	GetFloatingIP(id string) (fip *floatingips.FloatingIP, err error)

	AssociateFloatingIPToInstance(serverID string, opts floatingips.AssociateOpts) (err error)
	ListFloatingIPs() (fips []floatingips.FloatingIP, err error)
	ListL3FloatingIPs(opts l3floatingip.ListOpts) (fips []l3floatingip.FloatingIP, err error)
	CreateFloatingIP(opts floatingips.CreateOpts) (*floatingips.FloatingIP, error)
	CreateL3FloatingIP(opts l3floatingip.CreateOpts) (fip *l3floatingip.FloatingIP, err error)
}

type openstackCloud struct {
	cinderClient  *gophercloud.ServiceClient
	neutronClient *gophercloud.ServiceClient
	novaClient    *gophercloud.ServiceClient
	dnsClient     *gophercloud.ServiceClient
	lbClient      *gophercloud.ServiceClient
	tags          map[string]string
	region        string
}

var _ fi.Cloud = &openstackCloud{}

func NewOpenstackCloud(tags map[string]string, spec *kops.ClusterSpec) (OpenstackCloud, error) {
	config := vfs.OpenstackConfig{}

	authOption, err := config.GetCredential()
	if err != nil {
		return nil, err
	}

	/*
		provider, err := os.AuthenticatedClient(authOption)
		if err != nil {
			return nil, fmt.Errorf("error building openstack authenticated client: %v", err)
		}*/

	provider, err := os.NewClient(authOption.IdentityEndpoint)
	if err != nil {
		return nil, fmt.Errorf("error building openstack provider client: %v", err)
	}

	region, err := config.GetRegion()
	if err != nil {
		return nil, fmt.Errorf("error finding openstack region: %v", err)
	}

	tlsconfig := &tls.Config{}
	tlsconfig.InsecureSkipVerify = true
	transport := &http.Transport{TLSClientConfig: tlsconfig}
	provider.HTTPClient = http.Client{
		Transport: transport,
	}

	glog.V(2).Info("authenticating to keystone")

	err = os.Authenticate(provider, authOption)
	if err != nil {
		return nil, fmt.Errorf("error building openstack authenticated client: %v", err)
	}

	//TODO: maybe try v2, and v3?
	cinderClient, err := os.NewBlockStorageV2(provider, gophercloud.EndpointOpts{
		Type:   "volumev2",
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("error building cinder client: %v", err)
	}

	neutronClient, err := os.NewNetworkV2(provider, gophercloud.EndpointOpts{
		Type:   "network",
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("error building neutron client: %v", err)
	}

	novaClient, err := os.NewComputeV2(provider, gophercloud.EndpointOpts{
		Type:   "compute",
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("error building nova client: %v", err)
	}

	var dnsClient *gophercloud.ServiceClient
	if !dns.IsGossipHostname(tags[TagClusterName]) {
		//TODO: This should be replaced with the environment variable methods as done above
		endpointOpt, err := config.GetServiceConfig("Designate")
		if err != nil {
			return nil, err
		}

		dnsClient, err = os.NewDNSV2(provider, endpointOpt)
		if err != nil {
			return nil, fmt.Errorf("error building dns client: %v", err)
		}
	}

	lbClient, err := os.NewLoadBalancerV2(provider, gophercloud.EndpointOpts{
		Type:   "network",
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("error building lb client: %v", err)
	}

	c := &openstackCloud{
		cinderClient:  cinderClient,
		neutronClient: neutronClient,
		novaClient:    novaClient,
		lbClient:      lbClient,
		dnsClient:     dnsClient,
		tags:          tags,
		region:        region,
	}

	//TODO: Config setup would be better performed in create_cluster and moved to swift
	//    This will cause a new api version to need to be created
	if spec != nil {
		if spec.CloudConfig == nil {
			spec.CloudConfig = &kops.CloudConfiguration{}
		}
		spec.CloudConfig.Openstack = &kops.OpenstackConfiguration{}

		if spec.API.LoadBalancer != nil {

			network, err := c.GetExternalNetwork()
			if err != nil {
				return nil, fmt.Errorf("Failed to get external network for openstack: %v", err)
			}
			spec.CloudConfig.Openstack.Loadbalancer = &kops.OpenstackLoadbalancerConfig{
				FloatingNetwork: fi.String(network.Name),
				Method:          fi.String("ROUND_ROBIN"),
				Provider:        fi.String("haproxy"),
				UseOctavia:      fi.Bool(false),
			}
		}
		spec.CloudConfig.Openstack.Monitor = &kops.OpenstackMonitor{
			Delay:      fi.String("1m"),
			Timeout:    fi.String("30s"),
			MaxRetries: fi.Int(3),
		}
	}

	return c, nil
}

func (c *openstackCloud) ComputeClient() *gophercloud.ServiceClient {
	return c.novaClient
}

func (c *openstackCloud) BlockStorageClient() *gophercloud.ServiceClient {
	return c.cinderClient
}

func (c *openstackCloud) NetworkingClient() *gophercloud.ServiceClient {
	return c.neutronClient
}

func (c *openstackCloud) LoadBalancerClient() *gophercloud.ServiceClient {
	return c.lbClient
}

func (c *openstackCloud) DNSClient() *gophercloud.ServiceClient {
	return c.dnsClient
}

func (c *openstackCloud) Region() string {
	return c.region
}

func (c *openstackCloud) ProviderID() kops.CloudProviderID {
	return kops.CloudProviderOpenstack
}

func (c *openstackCloud) DNS() (dnsprovider.Interface, error) {
	provider, err := dnsprovider.GetDnsProvider(designate.ProviderName, nil)
	if err != nil {
		return nil, fmt.Errorf("Error building (Designate) DNS provider: %v", err)
	}
	return provider, nil
}

func (c *openstackCloud) FindVPCInfo(id string) (*fi.VPCInfo, error) {
	return nil, fmt.Errorf("openstackCloud::FindVPCInfo not implemented")
}

func (c *openstackCloud) DeleteGroup(g *cloudinstances.CloudInstanceGroup) error {
	return fmt.Errorf("openstackCloud::DeleteGroup not implemented")
}

func (c *openstackCloud) GetCloudGroups(cluster *kops.Cluster, instancegroups []*kops.InstanceGroup, warnUnmatched bool, nodes []v1.Node) (map[string]*cloudinstances.CloudInstanceGroup, error) {
	return nil, fmt.Errorf("openstackCloud::GetCloudGroups not implemented")
}

func (c *openstackCloud) GetCloudTags() map[string]string {
	return c.tags
}

func (c *openstackCloud) GetApiIngressStatus(cluster *kops.Cluster) ([]kops.ApiIngressStatus, error) {
	var ingresses []kops.ApiIngressStatus
	if cluster.Spec.MasterPublicName != "" {
		// Note that this must match OpenstackModel lb name
		glog.V(2).Infof("Querying Openstack to find Loadbalancers for API (%q)", cluster.Name)
		lbList, err := c.ListLBs(loadbalancers.ListOpts{
			Name: cluster.Spec.MasterPublicName,
		})
		if err != nil {
			return ingresses, fmt.Errorf("GetApiIngressStatus: Failed to list openstack loadbalancers: %v", err)
		}
		// Must Find Floating IP related to this lb
		fips, err := c.ListFloatingIPs()
		if err != nil {
			return ingresses, fmt.Errorf("GetApiIngressStatus: Failed to list floating IP's: %v", err)
		}

		for _, lb := range lbList {
			for _, fip := range fips {
				if fip.FixedIP == lb.VipAddress {

					ingresses = append(ingresses, kops.ApiIngressStatus{
						IP: fip.IP,
					})
				}
			}
		}
	}

	return ingresses, nil
}
