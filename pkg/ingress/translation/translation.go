// Copyright (c) 2022 Alibaba Group Holding Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package translation

import (
	"sync"

	istiomodel "istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/config"
	"istio.io/istio/pkg/config/schema/collection"
	"istio.io/istio/pkg/config/schema/gvk"
	"k8s.io/client-go/tools/cache"

	ingressconfig "github.com/alibaba/higress/pkg/ingress/config"
	"github.com/alibaba/higress/pkg/ingress/kube/common"
	. "github.com/alibaba/higress/pkg/ingress/log"
	"github.com/alibaba/higress/pkg/kube"
	"github.com/alibaba/higress/pkg/model"
)

var (
	_ istiomodel.ConfigStoreController = &IngressTranslation{}
	_ model.IngressStore               = &IngressTranslation{}
)

type IngressTranslation struct {
	ingressConfig      *ingressconfig.IngressConfig
	kingressConfig     *ingressconfig.KIngressConfig
	mutex              sync.RWMutex
	higressRouteCache  model.IngressRouteCollection
	higressDomainCache model.IngressDomainCollection
}

func NewIngressTranslation(localKubeClient kube.Client, xdsUpdater istiomodel.XDSUpdater, namespace, clusterId string) *IngressTranslation {
	if clusterId == "Kubernetes" {
		clusterId = ""
	}
	Config := &IngressTranslation{
		ingressConfig:  ingressconfig.NewIngressConfig(localKubeClient, xdsUpdater, namespace, clusterId),
		kingressConfig: ingressconfig.NewKIngressConfig(localKubeClient, xdsUpdater, namespace, clusterId),
	}
	return Config
}

func (m *IngressTranslation) AddLocalCluster(options common.Options) (common.IngressController, common.KIngressController) {
	if m.kingressConfig == nil {
		return m.ingressConfig.AddLocalCluster(options), nil
	}
	return m.ingressConfig.AddLocalCluster(options), m.kingressConfig.AddLocalCluster(options)
}

func (m *IngressTranslation) InitializeCluster(ingressController common.IngressController, kingressController common.KIngressController, stop <-chan struct{}) error {
	if err := m.ingressConfig.InitializeCluster(ingressController, stop); err != nil {
		return err
	}
	if kingressController == nil {
		return nil
	}
	if err := m.kingressConfig.InitializeCluster(kingressController, stop); err != nil {
		return err
	}
	return nil
}

func (m *IngressTranslation) RegisterEventHandler(kind config.GroupVersionKind, f istiomodel.EventHandler) {
	m.ingressConfig.RegisterEventHandler(kind, f)
	if m.kingressConfig != nil {
		m.kingressConfig.RegisterEventHandler(kind, f)
	}
}

func (m *IngressTranslation) HasSynced() bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	if !m.ingressConfig.HasSynced() {
		return false
	}
	if m.kingressConfig != nil {
		if !m.kingressConfig.HasSynced() {
			return false
		}
	}

	return true
}

func (m *IngressTranslation) Run(stop <-chan struct{}) {
	go m.ingressConfig.Run(stop)
	if m.kingressConfig != nil {
		go m.kingressConfig.Run(stop)
	}
}

func (m *IngressTranslation) SetWatchErrorHandler(f func(r *cache.Reflector, err error)) error {
	m.ingressConfig.SetWatchErrorHandler(f)
	if m.kingressConfig != nil {
		m.kingressConfig.SetWatchErrorHandler(f)
	}
	return nil
}

func (m *IngressTranslation) GetIngressRoutes() model.IngressRouteCollection {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	ingressRouteCache := m.ingressConfig.GetIngressRoutes()
	m.higressRouteCache = model.IngressRouteCollection{}
	m.higressRouteCache.Invalid = append(m.higressRouteCache.Invalid, ingressRouteCache.Invalid...)
	m.higressRouteCache.Valid = append(m.higressRouteCache.Valid, ingressRouteCache.Valid...)
	if m.kingressConfig != nil {
		kingressRouteCache := m.kingressConfig.GetIngressRoutes()
		m.higressRouteCache.Invalid = append(m.higressRouteCache.Invalid, kingressRouteCache.Invalid...)
		m.higressRouteCache.Valid = append(m.higressRouteCache.Valid, kingressRouteCache.Valid...)
	}

	return m.higressRouteCache

}

func (m *IngressTranslation) GetIngressDomains() model.IngressDomainCollection {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	ingressDomainCache := m.ingressConfig.GetIngressDomains()

	m.higressDomainCache = model.IngressDomainCollection{}
	m.higressDomainCache.Invalid = append(m.higressDomainCache.Invalid, ingressDomainCache.Invalid...)
	m.higressDomainCache.Valid = append(m.higressDomainCache.Valid, ingressDomainCache.Valid...)
	if m.kingressConfig != nil {
		kingressDomainCache := m.kingressConfig.GetIngressDomains()
		m.higressDomainCache.Invalid = append(m.higressDomainCache.Invalid, kingressDomainCache.Invalid...)
		m.higressDomainCache.Valid = append(m.higressDomainCache.Valid, kingressDomainCache.Valid...)
	}
	return m.higressDomainCache
}

func (m *IngressTranslation) Schemas() collection.Schemas {
	return common.IngressIR
}

func (m *IngressTranslation) Get(typ config.GroupVersionKind, name, namespace string) *config.Config {
	return nil
}

func (m *IngressTranslation) List(typ config.GroupVersionKind, namespace string) []config.Config {
	if typ != gvk.Gateway &&
		typ != gvk.VirtualService &&
		typ != gvk.DestinationRule &&
		typ != gvk.EnvoyFilter &&
		typ != gvk.ServiceEntry &&
		typ != gvk.WasmPlugin {
		return nil
	}

	// Currently, only support list all namespaces gateways or virtualservices.
	if namespace != "" {
		IngressLog.Warnf("ingress store only support type %s of all namespace.", typ)
		return nil
	}

	ingressConfig := m.ingressConfig.List(typ, namespace)
	if ingressConfig == nil {
		return nil
	}
	var higressConfig []config.Config
	higressConfig = append(higressConfig, ingressConfig...)
	if m.kingressConfig != nil {
		kingressConfig := m.kingressConfig.List(typ, namespace)
		if kingressConfig == nil {
			return nil
		}
		higressConfig = append(higressConfig, kingressConfig...)
	}
	return higressConfig
}

func (m *IngressTranslation) Create(config config.Config) (revision string, err error) {
	return "", common.ErrUnsupportedOp
}

func (m *IngressTranslation) Update(config config.Config) (newRevision string, err error) {
	return "", common.ErrUnsupportedOp
}

func (m *IngressTranslation) UpdateStatus(config config.Config) (newRevision string, err error) {
	return "", common.ErrUnsupportedOp
}

func (m *IngressTranslation) Patch(orig config.Config, patchFn config.PatchFunc) (string, error) {
	return "", common.ErrUnsupportedOp
}

func (m *IngressTranslation) Delete(typ config.GroupVersionKind, name, namespace string, resourceVersion *string) error {
	return common.ErrUnsupportedOp
}