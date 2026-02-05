package aws

import (
	"context"
	"reflect"
	"strings"

	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceName = "instance_profiles"

func DataSourceInstanceProfiles() datasource.DataSource {
	return &InstanceProfilesDataSource{}
}

var _ datasource.DataSourceWithConfigure = &InstanceProfilesDataSource{}

type InstanceProfilesDataSource struct {
	Client *common.DatabricksClient
}

type InstanceProfileData_SdkV2 struct {
	Name    types.String `tfsdk:"name"`
	Arn     types.String `tfsdk:"arn"`
	RoleArn types.String `tfsdk:"role_arn"`
	IsMeta  types.Bool   `tfsdk:"is_meta"`
}

func (InstanceProfileData_SdkV2) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{}
}

func (InstanceProfileData_SdkV2) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["name"] = attrs["name"].SetComputed()
	attrs["arn"] = attrs["arn"].SetComputed()
	attrs["role_arn"] = attrs["role_arn"].SetComputed()
	attrs["is_meta"] = attrs["is_meta"].SetComputed()
	return attrs
}

type InstanceProfilesList_SdkV2 struct {
	InstanceProfiles types.List `tfsdk:"instance_profiles"`
	tfschema.Namespace_SdkV2
}

func (InstanceProfilesList_SdkV2) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"instance_profiles": reflect.TypeOf(InstanceProfileData_SdkV2{}),
		"provider_config":   reflect.TypeOf(tfschema.ProviderConfig{}),
	}
}

func (InstanceProfilesList_SdkV2) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["instance_profiles"] = attrs["instance_profiles"].SetComputed().SetOptional()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (d *InstanceProfilesDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceName)
}

func (d *InstanceProfilesDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, InstanceProfilesList_SdkV2{}, func(cs tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		cs.ConfigureAsSdkV2Compatible()
		return cs
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *InstanceProfilesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *InstanceProfilesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceName)

	var data InstanceProfilesList_SdkV2
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, data.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceProfiles, err := w.InstanceProfiles.ListAll(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch instance profiles", err.Error())
		return
	}

	profiles := make([]InstanceProfileData_SdkV2, 0, len(instanceProfiles))
	for _, v := range instanceProfiles {
		arnSlices := strings.Split(v.InstanceProfileArn, "/")
		name := arnSlices[len(arnSlices)-1]
		profiles = append(profiles, InstanceProfileData_SdkV2{
			Name:    types.StringValue(name),
			Arn:     types.StringValue(v.InstanceProfileArn),
			RoleArn: types.StringValue(v.IamRoleArn),
			IsMeta:  types.BoolValue(v.IsMetaInstanceProfile),
		})
	}

	profilesList, listDiags := types.ListValueFrom(ctx, types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"name":     types.StringType,
			"arn":      types.StringType,
			"role_arn": types.StringType,
			"is_meta":  types.BoolType,
		},
	}, profiles)
	resp.Diagnostics.Append(listDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.InstanceProfiles = profilesList
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
