package rest

import (
	"context"

	"github.com/grafana/grafana/pkg/services/apiserver/utils"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
)

type DualWriterMode2 struct {
	DualWriter
}

// NewDualWriterMode2 returns a new DualWriter in mode 2.
// Mode 2 represents writing to LegacyStorage and Storage and reading from LegacyStorage.
func NewDualWriterMode2(legacy LegacyStorage, storage Storage) *DualWriterMode2 {
	return &DualWriterMode2{*NewDualWriter(legacy, storage)}
}

// Create overrides the behavior of the generic DualWriter and writes to LegacyStorage and Storage.
func (d *DualWriterMode2) Create(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	legacy, ok := d.Legacy.(rest.Creater)
	if !ok {
		return nil, errDualWriterCreaterMissing
	}

	created, err := legacy.Create(ctx, obj, createValidation, options)
	if err != nil {
		klog.FromContext(ctx).Error(err, "unable to create object in legacy storage", "mode", 2)
		return created, err
	}

	accessorCreated, err := meta.Accessor(created)
	if err != nil {
		return created, err
	}

	accessorOld, err := meta.Accessor(obj)
	if err != nil {
		return created, err
	}

	enrichObject(accessorOld, accessorCreated)

	// create method expects an empty resource version
	accessorCreated.SetResourceVersion("")
	accessorCreated.SetUID("")

	rsp, err := d.Storage.Create(ctx, created, createValidation, options)
	if err != nil {
		klog.FromContext(ctx).Error(err, "unable to create object in Storage", "mode", 2)
	}
	return rsp, err
}

// Get overrides the behavior of the generic DualWriter.
// It retrieves an object from Storage if possible, and if not it falls back to LegacyStorage.
func (d *DualWriterMode2) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	s, err := d.Storage.Get(ctx, name, &metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Info("object not found in storage", "name", name)
			return d.Legacy.Get(ctx, name, &metav1.GetOptions{})
		}
		klog.Error("unable to fetch object from storage", "error", err, "name", name)
		return d.Legacy.Get(ctx, name, &metav1.GetOptions{})
	}
	return s, nil
}

// List overrides the behavior of the generic DualWriter.
// It returns Storage entries if possible and falls back to LegacyStorage entries if not.
func (d *DualWriterMode2) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	legacy, ok := d.Legacy.(rest.Lister)
	if !ok {
		return nil, errDualWriterListerMissing
	}

	ll, err := legacy.List(ctx, options)
	if err != nil {
		return nil, err
	}
	legacyList, err := meta.ExtractList(ll)
	if err != nil {
		return nil, err
	}

	// Record the index of each LegacyStorage object so it can later be replaced by
	// an equivalent Storage object if it exists.
	optionsStorage, indexMap, err := parseList(legacyList)
	if err != nil {
		return nil, err
	}

	if optionsStorage.LabelSelector == nil {
		return ll, nil
	}

	sl, err := d.Storage.List(ctx, &optionsStorage)
	if err != nil {
		return nil, err
	}
	storageList, err := meta.ExtractList(sl)
	if err != nil {
		return nil, err
	}

	for _, obj := range storageList {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return nil, err
		}
		if legacyIndex, ok := indexMap[accessor.GetName()]; ok {
			legacyList[legacyIndex] = obj
		}
	}

	if err = meta.SetList(ll, legacyList); err != nil {
		return nil, err
	}
	return ll, nil
}

// DeleteCollection overrides the behavior of the generic DualWriter and deletes from both LegacyStorage and Storage.
func (d *DualWriterMode2) DeleteCollection(ctx context.Context, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions, listOptions *metainternalversion.ListOptions) (runtime.Object, error) {
	legacy, ok := d.Legacy.(rest.CollectionDeleter)
	if !ok {
		return nil, errDualWriterCollectionDeleterMissing
	}

	deleted, err := legacy.DeleteCollection(ctx, deleteValidation, options, listOptions)
	if err != nil {
		klog.FromContext(ctx).Error(err, "failed to delete collection successfully from legacy storage", "deletedObjects", deleted)
	}
	legacyList, err := meta.ExtractList(deleted)
	if err != nil {
		return nil, err
	}

	// Only the items deleted by the legacy DeleteCollection call are selected for deletion by Storage.
	optionsStorage, _, err := parseList(legacyList)
	if err != nil {
		return nil, err
	}
	if optionsStorage.LabelSelector == nil {
		return deleted, nil
	}

	res, err := d.Storage.DeleteCollection(ctx, deleteValidation, options, &optionsStorage)
	if err != nil {
		klog.FromContext(ctx).Error(err, "failed to delete collection successfully from Storage", "deletedObjects", res)
	}

	return res, err
}

func (d *DualWriterMode2) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	legacy, ok := d.Legacy.(rest.GracefulDeleter)
	if !ok {
		return nil, false, errDualWriterDeleterMissing
	}

	deletedLS, async, err := legacy.Delete(ctx, name, deleteValidation, options)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.FromContext(ctx).Error(err, "could not delete from legacy store", "mode", 2)
			return deletedLS, async, err
		}
	}

	_, _, errUS := d.Storage.Delete(ctx, name, deleteValidation, options)
	if errUS != nil {
		if !apierrors.IsNotFound(errUS) {
			klog.FromContext(ctx).Error(errUS, "could not delete from duplicate storage", "mode", 2, "name", name)
		}
	}

	return deletedLS, async, err
}

// Update overrides the generic behavior of the Storage and writes first to the legacy storage and then to storage.
func (d *DualWriterMode2) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	legacy, ok := d.Legacy.(rest.Updater)
	if !ok {
		return nil, false, errDualWriterUpdaterMissing
	}

	var notFound bool

	// get old and new (updated) object so they can be stored in legacy store
	old, err := d.Storage.Get(ctx, name, &metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.FromContext(ctx).Error(err, "could not get object", "mode", Mode2)
			return nil, false, err
		}
		klog.FromContext(ctx).Error(err, "object not found for update, creating one", "mode", Mode2)
		notFound = true
	}

	// obj can be populated in case it's found or empty in case it's not found
	updated, err := objInfo.UpdatedObject(ctx, old)
	if err != nil {
		return nil, false, err
	}

	obj, created, err := legacy.Update(ctx, name, &updateWrapper{upstream: objInfo, updated: updated}, createValidation, updateValidation, forceAllowCreate, options)
	if err != nil {
		klog.FromContext(ctx).Error(err, "could not update in legacy storage", "mode", Mode2)
		return obj, created, err
	}

	if notFound {
		return d.Storage.Update(ctx, name, objInfo, createValidation, updateValidation, forceAllowCreate, options)
	}

	accessor, err := meta.Accessor(obj)
	if err != nil {
		return nil, false, err
	}

	// only if object exists
	accessorOld, err := meta.Accessor(old)
	if err != nil {
		return nil, false, err
	}

	enrichObject(accessorOld, accessor)

	// keep the same UID and resource_version
	accessor.SetResourceVersion(accessorOld.GetResourceVersion())
	accessor.SetUID(accessorOld.GetUID())
	objInfo = &updateWrapper{
		upstream: objInfo,
		updated:  obj,
	}

	// TODO: relies on GuaranteedUpdate creating the object if
	// it doesn't exist: https://github.com/grafana/grafana/pull/85206
	return d.Storage.Update(ctx, name, objInfo, createValidation, updateValidation, forceAllowCreate, options)
}

func parseList(legacyList []runtime.Object) (metainternalversion.ListOptions, map[string]int, error) {
	options := metainternalversion.ListOptions{}
	originKeys := []string{}
	indexMap := map[string]int{}

	for i, obj := range legacyList {
		metaAccessor, err := utils.MetaAccessor(obj)
		if err != nil {
			return options, nil, err
		}
		originKeys = append(originKeys, metaAccessor.GetOriginKey())

		accessor, err := meta.Accessor(obj)
		if err != nil {
			return options, nil, err
		}
		indexMap[accessor.GetName()] = i
	}

	if len(originKeys) == 0 {
		return options, nil, nil
	}

	r, err := labels.NewRequirement(utils.AnnoKeyOriginKey, selection.In, originKeys)
	if err != nil {
		return options, nil, err
	}
	options.LabelSelector = labels.NewSelector().Add(*r)

	return options, indexMap, nil
}

func enrichObject(accessorO, accessorC metav1.Object) {
	accessorC.SetLabels(accessorO.GetLabels())

	ac := accessorC.GetAnnotations()
	for k, v := range accessorO.GetAnnotations() {
		ac[k] = v
	}
	accessorC.SetAnnotations(ac)
}
