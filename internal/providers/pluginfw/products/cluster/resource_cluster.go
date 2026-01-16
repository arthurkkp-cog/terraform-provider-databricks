package cluster

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/apierr"
	"github.com/databricks/databricks-sdk-go/service/compute"
	"github.com/databricks/terraform-provider-databricks/clusters"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/converters"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/databricks/terraform-provider-databricks/internal/service/compute_tf"
	"github.com/databricks/terraform-provider-databricks/libraries"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const resourceName = "cluster"
const defaultProvisionTimeout = 30 * time.Minute

var _ resource.ResourceWithConfigure = &ClusterResource{}

func ResourceCluster() resource.Resource {
	return &ClusterResource{}
}

type ClusterSpecExtended struct {
	compute_tf.ClusterSpec_SdkV2
	tfschema.Namespace_SdkV2
	ID                     types.String `tfsdk:"id"`
	ClusterId              types.String `tfsdk:"cluster_id"`
	DefaultTags            types.Map    `tfsdk:"default_tags"`
	State                  types.String `tfsdk:"state"`
	Url                    types.String `tfsdk:"url"`
	IsPinned               types.Bool   `tfsdk:"is_pinned"`
	NoWait                 types.Bool   `tfsdk:"no_wait"`
	IdempotencyToken       types.String `tfsdk:"idempotency_token"`
	Library                types.List   `tfsdk:"library"`
	ClusterMountInfo       types.List   `tfsdk:"cluster_mount_info"`
	AutoterminationMinutes types.Int64  `tfsdk:"autotermination_minutes"`
}

var _ pluginfwcommon.ComplexFieldTypeProvider = ClusterSpecExtended{}

func (c ClusterSpecExtended) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	types := c.ClusterSpec_SdkV2.GetComplexFieldTypes(ctx)
	types["provider_config"] = reflect.TypeOf(tfschema.ProviderConfig{})
	types["library"] = reflect.TypeOf(compute_tf.Library_SdkV2{})
	types["cluster_mount_info"] = reflect.TypeOf(MountInfo_SdkV2{})
	return types
}

type MountInfo_SdkV2 struct {
	NetworkFilesystemInfo types.List   `tfsdk:"network_filesystem_info"`
	RemoteMountDirPath    types.String `tfsdk:"remote_mount_dir_path"`
	LocalMountDirPath     types.String `tfsdk:"local_mount_dir_path"`
}

func (m MountInfo_SdkV2) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"network_filesystem_info": reflect.TypeOf(NetworkFilesystemInfo_SdkV2{}),
	}
}

func (m MountInfo_SdkV2) ToObjectValue(ctx context.Context) types.Object {
	return types.ObjectValueMust(
		m.Type(ctx).(types.ObjectType).AttrTypes,
		map[string]attr.Value{
			"network_filesystem_info": m.NetworkFilesystemInfo,
			"remote_mount_dir_path":   m.RemoteMountDirPath,
			"local_mount_dir_path":    m.LocalMountDirPath,
		})
}

func (m MountInfo_SdkV2) Type(ctx context.Context) attr.Type {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"network_filesystem_info": types.ListType{ElemType: NetworkFilesystemInfo_SdkV2{}.Type(ctx)},
			"remote_mount_dir_path":   types.StringType,
			"local_mount_dir_path":    types.StringType,
		},
	}
}

func (m MountInfo_SdkV2) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["network_filesystem_info"] = attrs["network_filesystem_info"].SetRequired()
	attrs["remote_mount_dir_path"] = attrs["remote_mount_dir_path"].SetOptional()
	attrs["local_mount_dir_path"] = attrs["local_mount_dir_path"].SetRequired()
	return attrs
}

type NetworkFilesystemInfo_SdkV2 struct {
	ServerAddress types.String `tfsdk:"server_address"`
	MountOptions  types.String `tfsdk:"mount_options"`
}

func (n NetworkFilesystemInfo_SdkV2) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{}
}

func (n NetworkFilesystemInfo_SdkV2) ToObjectValue(ctx context.Context) types.Object {
	return types.ObjectValueMust(
		n.Type(ctx).(types.ObjectType).AttrTypes,
		map[string]attr.Value{
			"server_address": n.ServerAddress,
			"mount_options":  n.MountOptions,
		})
}

func (n NetworkFilesystemInfo_SdkV2) Type(ctx context.Context) attr.Type {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"server_address": types.StringType,
			"mount_options":  types.StringType,
		},
	}
}

func (n NetworkFilesystemInfo_SdkV2) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["server_address"] = attrs["server_address"].SetRequired()
	attrs["mount_options"] = attrs["mount_options"].SetOptional()
	return attrs
}

type ClusterResource struct {
	Client *common.DatabricksClient
}

func (r *ClusterResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(resourceName)
}

func (r *ClusterResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs, blocks := tfschema.ResourceStructToSchemaMap(ctx, ClusterSpecExtended{}, func(c tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		c.ConfigureAsSdkV2Compatible()

		c.SetRequired("spark_version")

		c.SetComputed("enable_elastic_disk")
		c.SetComputed("enable_local_disk_encryption")
		c.SetComputed("node_type_id")
		c.SetComputed("driver_node_type_id")
		c.SetComputed("driver_instance_pool_id")

		c.SetDeprecated(clusters.DbfsDeprecationWarning, "init_scripts", "dbfs")
		c.SetRequired("init_scripts", "dbfs", "destination")
		c.SetRequired("init_scripts", "s3", "destination")
		c.SetRequired("init_scripts", "volumes", "destination")
		c.SetRequired("init_scripts", "workspace", "destination")
		c.SetRequired("workload_type", "clients")
		c.SetRequired("docker_image", "url")
		c.SetRequired("docker_image", "basic_auth", "password")
		c.SetSensitive("docker_image", "basic_auth", "password")
		c.SetRequired("docker_image", "basic_auth", "username")
		c.SetRequired("cluster_log_conf", "dbfs", "destination")
		c.SetRequired("cluster_log_conf", "s3", "destination")
		c.SetRequired("cluster_log_conf", "volumes", "destination")

		c.SetDeprecated(clusters.EggDeprecationWarning, "library", "egg")

		c.SetOptional("id")
		c.SetComputed("id")
		c.SetComputed("cluster_id")
		c.SetComputed("default_tags")
		c.SetComputed("state")
		c.SetComputed("url")

		c.SetOptional("is_pinned")
		c.SetOptional("no_wait")
		c.SetOptional("idempotency_token")
		c.AddPlanModifier(stringplanmodifier.RequiresReplace(), "idempotency_token")
		c.SetOptional("library")
		c.SetOptional("cluster_mount_info")
		c.SetOptional("autotermination_minutes")

		c.AddValidator(listvalidator.SizeAtMost(1), "provider_config")
		c.AddValidator(listvalidator.SizeAtMost(10), "ssh_public_keys")
		c.AddValidator(listvalidator.SizeAtMost(10), "init_scripts")

		return c
	})

	resp.Schema = schema.Schema{
		Description: "Terraform schema for Databricks Cluster",
		Attributes:  attrs,
		Blocks:      blocks,
	}
}

func (r *ClusterResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if r.Client == nil {
		r.Client = pluginfwcommon.ConfigureResource(req, resp)
	}
}

func (r *ClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var plan ClusterSpecExtended
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, plan.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var createClusterRequest compute.CreateCluster
	resp.Diagnostics.Append(converters.TfSdkToGoSdkStruct(ctx, plan, &createClusterRequest)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createClusterRequest.AutoterminationMinutes = int(plan.AutoterminationMinutes.ValueInt64())

	if err := clusters.ModifyRequestOnInstancePool(&createClusterRequest); err != nil {
		resp.Diagnostics.AddError("failed to modify request for instance pool", err.Error())
		return
	}

	clusters.SetForceSendFieldsForCluster(&createClusterRequest, nil)

	clusterWaiter, err := w.Clusters.Create(ctx, createClusterRequest)
	if err != nil {
		resp.Diagnostics.AddError("failed to create cluster", err.Error())
		return
	}

	clusterId := clusterWaiter.ClusterId

	isPinned := plan.IsPinned.ValueBool()
	if isPinned {
		if err := w.Clusters.PinByClusterId(ctx, clusterId); err != nil {
			resp.Diagnostics.AddError("failed to pin cluster", err.Error())
			return
		}
	}

	libs := r.getLibrariesFromPlan(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(libs) > 0 {
		if err := w.Libraries.Install(ctx, compute.InstallLibraries{
			ClusterId: clusterId,
			Libraries: libs,
		}); err != nil {
			resp.Diagnostics.AddError("failed to install libraries", err.Error())
			return
		}
	}

	noWait := plan.NoWait.ValueBool()
	if !noWait {
		clusterInfo, err := clusterWaiter.GetWithTimeout(defaultProvisionTimeout)
		if err != nil {
			deleteErr := w.Clusters.PermanentDeleteByClusterId(ctx, clusterId)
			if deleteErr != nil {
				resp.Diagnostics.AddError("failed to create cluster and failed to delete during cleanup",
					fmt.Sprintf("create error: %v, delete error: %v", err, deleteErr))
				return
			}
			resp.Diagnostics.AddError("failed to create cluster", err.Error())
			return
		}

		if len(libs) > 0 {
			_, err := libraries.WaitForLibrariesInstalledSdk(ctx, w, compute.Wait{
				ClusterID: clusterId,
				IsRunning: clusterInfo.IsRunningOrResizing(),
				IsRefresh: false,
			}, defaultProvisionTimeout)
			if err != nil {
				resp.Diagnostics.AddError("failed to wait for libraries installation", err.Error())
				return
			}
		}
	}

	newState, diags := r.readClusterState(ctx, w, clusterId, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *ClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var state ClusterSpecExtended
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, state.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterId := state.ClusterId.ValueString()
	if clusterId == "" {
		clusterId = state.ID.ValueString()
	}

	newState, diags := r.readClusterState(ctx, w, clusterId, state)
	if diags.HasError() {
		for _, d := range diags {
			if d.Severity() == diag.SeverityError && isClusterMissing(d) {
				resp.State.RemoveResource(ctx)
				return
			}
		}
		resp.Diagnostics.Append(diags...)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func isClusterMissing(d diag.Diagnostic) bool {
	return d.Summary() == "cluster not found"
}

func (r *ClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var plan ClusterSpecExtended
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state ClusterSpecExtended
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, plan.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterId := state.ClusterId.ValueString()
	if clusterId == "" {
		clusterId = state.ID.ValueString()
	}

	var editCluster compute.EditCluster
	resp.Diagnostics.Append(converters.TfSdkToGoSdkStruct(ctx, plan, &editCluster)...)
	if resp.Diagnostics.HasError() {
		return
	}
	editCluster.ClusterId = clusterId
	editCluster.AutoterminationMinutes = int(plan.AutoterminationMinutes.ValueInt64())

	if err := clusters.ModifyRequestOnInstancePool(&editCluster); err != nil {
		resp.Diagnostics.AddError("failed to modify request for instance pool", err.Error())
		return
	}

	clusters.SetForceSendFieldsForCluster(&editCluster, nil)

	_, err := w.Clusters.Edit(ctx, editCluster)
	if err != nil {
		resp.Diagnostics.AddError("failed to edit cluster", err.Error())
		return
	}

	oldPinned := state.IsPinned.ValueBool()
	newPinned := plan.IsPinned.ValueBool()
	if oldPinned != newPinned {
		if newPinned {
			err = w.Clusters.PinByClusterId(ctx, clusterId)
		} else {
			err = w.Clusters.UnpinByClusterId(ctx, clusterId)
		}
		if err != nil {
			resp.Diagnostics.AddError("failed to update pinned status", err.Error())
			return
		}
	}

	planLibs := r.getLibrariesFromPlan(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	stateLibs := r.getLibrariesFromPlan(ctx, state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if len(planLibs) > 0 || len(stateLibs) > 0 {
		libsClusterStatus, err := w.Libraries.ClusterStatusByClusterId(ctx, clusterId)
		if err != nil {
			resp.Diagnostics.AddError("failed to get library status", err.Error())
			return
		}

		libsToInstall, libsToUninstall := libraries.GetLibrariesToInstallAndUninstall(planLibs, libsClusterStatus)

		if len(libsToUninstall) > 0 || len(libsToInstall) > 0 {
			clusterInfo, err := w.Clusters.GetByClusterId(ctx, clusterId)
			if err != nil {
				resp.Diagnostics.AddError("failed to get cluster info", err.Error())
				return
			}

			if !clusterInfo.IsRunningOrResizing() {
				if _, err = w.Clusters.StartByClusterIdAndWait(ctx, clusterId); err != nil {
					resp.Diagnostics.AddError("failed to start cluster for library update", err.Error())
					return
				}
			}

			err = w.Libraries.UpdateAndWait(ctx, compute.Update{
				ClusterId: clusterId,
				Install:   libsToInstall,
				Uninstall: libsToUninstall,
			})
			if err != nil {
				resp.Diagnostics.AddError("failed to update libraries", err.Error())
				return
			}

			if clusterInfo.State == compute.StateTerminated {
				if err = w.Clusters.DeleteByClusterId(ctx, clusterId); err != nil {
					resp.Diagnostics.AddError("failed to terminate cluster after library update", err.Error())
					return
				}
			}
		}
	}

	newState, diags := r.readClusterState(ctx, w, clusterId, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *ClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var state ClusterSpecExtended
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, state.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterId := state.ClusterId.ValueString()
	if clusterId == "" {
		clusterId = state.ID.ValueString()
	}

	err := w.Clusters.PermanentDeleteByClusterId(ctx, clusterId)
	if err == nil || apierr.IsMissing(err) {
		return
	}

	if strings.Contains(err.Error(), "unpin the cluster first") {
		err = w.Clusters.UnpinByClusterId(ctx, clusterId)
		if err != nil {
			resp.Diagnostics.AddError("failed to unpin cluster before deletion", err.Error())
			return
		}
		err = w.Clusters.PermanentDeleteByClusterId(ctx, clusterId)
		if err != nil && !apierr.IsMissing(err) {
			resp.Diagnostics.AddError("failed to delete cluster", err.Error())
		}
		return
	}

	resp.Diagnostics.AddError("failed to delete cluster", err.Error())
}

func (r *ClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *ClusterResource) readClusterState(ctx context.Context, w *databricks.WorkspaceClient, clusterId string, existingState ClusterSpecExtended) (ClusterSpecExtended, diag.Diagnostics) {
	var d diag.Diagnostics

	clusterInfo, err := w.Clusters.GetByClusterId(ctx, clusterId)
	if err != nil {
		if apierr.IsMissing(err) {
			d.AddError("cluster not found", fmt.Sprintf("cluster %s not found", clusterId))
		} else {
			d.AddError("failed to get cluster", err.Error())
		}
		return ClusterSpecExtended{}, d
	}

	var newState ClusterSpecExtended
	d.Append(converters.GoSdkToTfSdkStruct(ctx, clusterInfo, &newState)...)
	if d.HasError() {
		return ClusterSpecExtended{}, d
	}

	newState.ID = types.StringValue(clusterId)
	newState.ClusterId = types.StringValue(clusterId)
	newState.State = types.StringValue(string(clusterInfo.State))
	newState.Url = types.StringValue(r.Client.FormatURL("#setting/clusters/", clusterId, "/configuration"))

	if clusterInfo.DefaultTags != nil {
		defaultTagsMap := make(map[string]attr.Value)
		for k, v := range clusterInfo.DefaultTags {
			defaultTagsMap[k] = types.StringValue(v)
		}
		newState.DefaultTags = types.MapValueMust(types.StringType, defaultTagsMap)
	} else {
		newState.DefaultTags = types.MapNull(types.StringType)
	}

	isPinned, err := r.isPinned(ctx, w, clusterId)
	if err != nil {
		d.AddError("failed to check pinned status", err.Error())
		return ClusterSpecExtended{}, d
	}
	newState.IsPinned = types.BoolValue(isPinned)

	newState.NoWait = existingState.NoWait
	newState.IdempotencyToken = existingState.IdempotencyToken
	newState.ProviderConfig = existingState.ProviderConfig
	newState.AutoterminationMinutes = types.Int64Value(int64(clusterInfo.AutoterminationMinutes))

	shouldSkipLibrariesRead := !common.IsExporter(ctx)
	existingLibCount := 0
	if !existingState.Library.IsNull() && !existingState.Library.IsUnknown() {
		existingLibCount = len(existingState.Library.Elements())
	}

	if existingLibCount == 0 && shouldSkipLibrariesRead {
		newState.Library = existingState.Library
	} else {
		libsClusterStatus, err := libraries.WaitForLibrariesInstalledSdk(ctx, w, compute.Wait{
			ClusterID: clusterId,
			IsRunning: clusterInfo.IsRunningOrResizing(),
			IsRefresh: true,
		}, defaultProvisionTimeout)
		if err != nil {
			d.AddError("failed to get library status", err.Error())
			return ClusterSpecExtended{}, d
		}

		libList := libsClusterStatus.ToLibraryList()
		if len(libList.Libraries) > 0 {
			var tfLibs []compute_tf.Library_SdkV2
			for _, lib := range libList.Libraries {
				var tfLib compute_tf.Library_SdkV2
				d.Append(converters.GoSdkToTfSdkStruct(ctx, lib, &tfLib)...)
				if d.HasError() {
					return ClusterSpecExtended{}, d
				}
				tfLibs = append(tfLibs, tfLib)
			}
			libValues := make([]attr.Value, len(tfLibs))
			for i, lib := range tfLibs {
				libValues[i] = lib.ToObjectValue(ctx)
			}
			newState.Library = types.ListValueMust(compute_tf.Library_SdkV2{}.Type(ctx), libValues)
		} else {
			newState.Library = types.ListNull(compute_tf.Library_SdkV2{}.Type(ctx))
		}
	}

	newState.ClusterMountInfo = existingState.ClusterMountInfo

	return newState, d
}

func (r *ClusterResource) isPinned(ctx context.Context, w *databricks.WorkspaceClient, clusterId string) (bool, error) {
	clusterDetails := w.Clusters.List(ctx, compute.ListClustersRequest{
		FilterBy: &compute.ListClustersFilterBy{
			IsPinned: true,
		},
		PageSize: 100,
	})

	for clusterDetails.HasNext(ctx) {
		detail, err := clusterDetails.Next(ctx)
		if err != nil {
			return false, err
		}
		if detail.ClusterId == clusterId {
			return true, nil
		}
	}
	return false, nil
}

func (r *ClusterResource) getLibrariesFromPlan(ctx context.Context, plan ClusterSpecExtended, diags *diag.Diagnostics) []compute.Library {
	if plan.Library.IsNull() || plan.Library.IsUnknown() {
		return nil
	}

	var tfLibs []compute_tf.Library_SdkV2
	diags.Append(plan.Library.ElementsAs(ctx, &tfLibs, true)...)
	if diags.HasError() {
		return nil
	}

	var libs []compute.Library
	for _, tfLib := range tfLibs {
		var lib compute.Library
		diags.Append(converters.TfSdkToGoSdkStruct(ctx, tfLib, &lib)...)
		if diags.HasError() {
			return nil
		}
		libs = append(libs, lib)
	}

	return libs
}
