package registered_model

import (
	"context"
	"reflect"

	"github.com/databricks/databricks-sdk-go/apierr"
	"github.com/databricks/databricks-sdk-go/service/catalog"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/converters"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/databricks/terraform-provider-databricks/internal/service/catalog_tf"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const resourceName = "registered_model"

var _ resource.ResourceWithConfigure = &RegisteredModelResource{}

func ResourceRegisteredModel() resource.Resource {
	return &RegisteredModelResource{}
}

type RegisteredModelInfoExtended struct {
	catalog_tf.RegisteredModelInfo_SdkV2
	tfschema.Namespace_SdkV2
	ID types.String `tfsdk:"id"`
}

var _ pluginfwcommon.ComplexFieldTypeProvider = RegisteredModelInfoExtended{}

func (r RegisteredModelInfoExtended) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	types := r.RegisteredModelInfo_SdkV2.GetComplexFieldTypes(ctx)
	types["provider_config"] = reflect.TypeOf(tfschema.ProviderConfig{})
	return types
}

type RegisteredModelResource struct {
	Client *common.DatabricksClient
}

func (r *RegisteredModelResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(resourceName)
}

func (r *RegisteredModelResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs, blocks := tfschema.ResourceStructToSchemaMap(ctx, RegisteredModelInfoExtended{}, func(c tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		c.ConfigureAsSdkV2Compatible()

		c.SetRequired("name")
		c.SetRequired("catalog_name")
		c.SetRequired("schema_name")

		c.AddPlanModifier(stringplanmodifier.RequiresReplace(), "name")
		c.AddPlanModifier(stringplanmodifier.RequiresReplace(), "catalog_name")
		c.AddPlanModifier(stringplanmodifier.RequiresReplace(), "schema_name")
		c.AddPlanModifier(stringplanmodifier.RequiresReplaceIfConfigured(), "storage_location")

		c.AddPlanModifier(caseInsensitivePlanModifier{}, "name")
		c.AddPlanModifier(caseInsensitivePlanModifier{}, "catalog_name")
		c.AddPlanModifier(caseInsensitivePlanModifier{}, "schema_name")

		c.SetReadOnly("created_at")
		c.SetReadOnly("created_by")
		c.SetReadOnly("full_name")
		c.SetReadOnly("metastore_id")
		c.SetReadOnly("updated_at")
		c.SetReadOnly("updated_by")
		c.SetReadOnly("browse_only")

		c.SetComputed("storage_location")

		c.SetComputed("id")
		c.SetOptional("id")

		c.AddValidator(listvalidator.SizeAtMost(1), "provider_config")

		return c
	})
	resp.Schema = schema.Schema{
		Description: "Terraform schema for Databricks Registered Model",
		Attributes:  attrs,
		Blocks:      blocks,
	}
}

func (r *RegisteredModelResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if r.Client == nil && req.ProviderData != nil {
		r.Client = pluginfwcommon.ConfigureResource(req, resp)
	}
}

func (r *RegisteredModelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("full_name"), req, resp)
}

func (r *RegisteredModelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var plan RegisteredModelInfoExtended
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

	var createReq catalog.CreateRegisteredModelRequest
	resp.Diagnostics.Append(converters.TfSdkToGoSdkStruct(ctx, plan, &createReq)...)
	if resp.Diagnostics.HasError() {
		return
	}

	model, err := w.RegisteredModels.Create(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("failed to create registered model", err.Error())
		return
	}

	if !plan.Owner.IsNull() && plan.Owner.ValueString() != "" {
		_, err = w.RegisteredModels.Update(ctx, catalog.UpdateRegisteredModelRequest{
			FullName: model.FullName,
			Owner:    plan.Owner.ValueString(),
		})
		if err != nil {
			resp.Diagnostics.AddError("failed to update registered model owner", err.Error())
			return
		}
		model, err = w.RegisteredModels.GetByFullName(ctx, model.FullName)
		if err != nil {
			resp.Diagnostics.AddError("failed to get registered model after owner update", err.Error())
			return
		}
	}

	var newState RegisteredModelInfoExtended
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, model, &newState)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newState.ID = newState.FullName
	newState.ProviderConfig = plan.ProviderConfig

	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *RegisteredModelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var state RegisteredModelInfoExtended
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

	fullName := state.FullName.ValueString()
	if fullName == "" {
		fullName = state.ID.ValueString()
	}

	model, err := w.RegisteredModels.GetByFullName(ctx, fullName)
	if err != nil {
		if apierr.IsMissing(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("failed to get registered model", err.Error())
		return
	}

	var newState RegisteredModelInfoExtended
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, model, &newState)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newState.ID = newState.FullName
	newState.ProviderConfig = state.ProviderConfig

	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *RegisteredModelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var plan RegisteredModelInfoExtended
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state RegisteredModelInfoExtended
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

	fullName := state.FullName.ValueString()

	ownerChanged := !plan.Owner.Equal(state.Owner)
	if ownerChanged && !plan.Owner.IsNull() && plan.Owner.ValueString() != "" {
		_, err := w.RegisteredModels.Update(ctx, catalog.UpdateRegisteredModelRequest{
			FullName: fullName,
			Owner:    plan.Owner.ValueString(),
		})
		if err != nil {
			resp.Diagnostics.AddError("failed to update registered model owner", err.Error())
			return
		}
	}

	commentChanged := !plan.Comment.Equal(state.Comment)
	if commentChanged {
		updateReq := catalog.UpdateRegisteredModelRequest{
			FullName: fullName,
			Comment:  plan.Comment.ValueString(),
		}
		if plan.Comment.ValueString() == "" {
			updateReq.ForceSendFields = append(updateReq.ForceSendFields, "Comment")
		}
		_, err := w.RegisteredModels.Update(ctx, updateReq)
		if err != nil {
			if ownerChanged {
				oldOwner := state.Owner.ValueString()
				newOwner := plan.Owner.ValueString()
				_, rollbackErr := w.RegisteredModels.Update(ctx, catalog.UpdateRegisteredModelRequest{
					FullName: fullName,
					Owner:    oldOwner,
				})
				if rollbackErr != nil {
					resp.Diagnostics.AddError("failed to update registered model and rollback owner", common.OwnerRollbackError(err, rollbackErr, oldOwner, newOwner).Error())
					return
				}
			}
			resp.Diagnostics.AddError("failed to update registered model", err.Error())
			return
		}
	}

	model, err := w.RegisteredModels.GetByFullName(ctx, fullName)
	if err != nil {
		resp.Diagnostics.AddError("failed to get registered model after update", err.Error())
		return
	}

	var newState RegisteredModelInfoExtended
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, model, &newState)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newState.ID = newState.FullName
	newState.ProviderConfig = plan.ProviderConfig

	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *RegisteredModelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var state RegisteredModelInfoExtended
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

	err := w.RegisteredModels.DeleteByFullName(ctx, state.FullName.ValueString())
	if err != nil && !apierr.IsMissing(err) {
		resp.Diagnostics.AddError("failed to delete registered model", err.Error())
	}
}

type caseInsensitivePlanModifier struct{}

func (m caseInsensitivePlanModifier) Description(ctx context.Context) string {
	return "Suppresses diff when values differ only in case"
}

func (m caseInsensitivePlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m caseInsensitivePlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() {
		return
	}
	if common.EqualFoldDiffSuppress("", req.StateValue.ValueString(), req.PlanValue.ValueString(), nil) {
		resp.PlanValue = req.StateValue
	}
}
