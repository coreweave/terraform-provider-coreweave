package sandbox

import (
	"context"
	"fmt"
	"net/netip"

	sandboxv1 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func optionalString(description string) schema.StringAttribute {
	return schema.StringAttribute{Optional: true, MarkdownDescription: description}
}

func optionalBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, MarkdownDescription: description}
}

func optionalInt(description string) schema.Int64Attribute {
	return schema.Int64Attribute{Optional: true, MarkdownDescription: description, Validators: []validator.Int64{int64validator.AtLeast(0)}}
}

func optionalStrings(description string) schema.SetAttribute {
	return schema.SetAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: description}
}

func optionalMap(description string) schema.MapAttribute {
	return schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: description}
}

func optionalObject(description string, attributes map[string]schema.Attribute) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Optional: true, Attributes: attributes, MarkdownDescription: description}
}

func optionalObjects(description string, attributes map[string]schema.Attribute) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{Optional: true, NestedObject: schema.NestedAttributeObject{Attributes: attributes}, MarkdownDescription: description}
}

func enumAttribute(description string, values map[int32]string) schema.StringAttribute {
	return schema.StringAttribute{
		Optional:            true,
		MarkdownDescription: description + " Values: " + coreweave.EnumMarkdownValues(values, true) + ".",
		Validators:          []validator.String{stringvalidator.OneOf(coreweave.EnumValues(values, true)...)},
	}
}

func runnerSpecAttribute() schema.SingleNestedAttribute {
	channel := enumAttribute("Automatic update channel. Defaults to stable.", sandboxv1.ReleaseChannel_name)
	channel.Computed = true
	channel.Default = stringdefault.StaticString("RELEASE_CHANNEL_STABLE")
	return schema.SingleNestedAttribute{
		Optional: true, Computed: true,
		MarkdownDescription: "Desired runner configuration. When omitted, the server's defaults are adopted. Removing an optional setting inside this object resets that setting through an explicit update mask.",
		Attributes: map[string]schema.Attribute{
			"release_channel": channel,
			"maintenance_policy": optionalObject("Update scheduling in UTC. Exclusions take precedence over windows.", map[string]schema.Attribute{
				"windows": optionalObjects("Recurring update windows. An empty list permits updates at any time outside exclusions.", map[string]schema.Attribute{
					"cron":             schema.StringAttribute{Required: true, MarkdownDescription: "Five-field cron expression in UTC, for example `0 2 * * SAT`.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
					"duration_seconds": schema.Int64Attribute{Required: true, MarkdownDescription: "Window duration in seconds (60–604800).", Validators: []validator.Int64{int64validator.Between(60, 604800)}},
				}),
				"exclusions": optionalObjects("One-time periods during which updates cannot start.", map[string]schema.Attribute{
					"start_time": schema.StringAttribute{Required: true, MarkdownDescription: "Inclusive RFC 3339 start timestamp."},
					"end_time":   schema.StringAttribute{Required: true, MarkdownDescription: "Exclusive RFC 3339 end timestamp, after start_time."},
					"reason":     optionalString("Reason for the exclusion."),
				}),
			}),
			"overrides":               runnerOverridesAttribute(),
			"enforce_resource_limits": optionalBool("Require sandboxes to declare both memory and CPU limits."),
			"volumes": optionalObject("Registered volume support. These settings take precedence over corresponding deployment environment entries.", map[string]schema.Attribute{
				"enabled":              optionalBool("Enable registered volume mounts; the runner's PVC/PV permission preflight must also pass."),
				"csi_driver_allowlist": optionalStrings("Allowed CSI drivers. Empty uses the platform default (`csi.vastdata.com`)."),
			}),
			"tenant_metrics": optionalObject("Per-sandbox tenant usage collection. Overrides corresponding deployment environment entries.", map[string]schema.Attribute{
				"enabled": optionalBool("Enable the tenant metrics collector."),
				"image":   optionalString("Release-pinned collector image. Empty retains the deployment environment's image."),
			}),
			"image_pull": optionalObject("Workload container image pull behavior.", map[string]schema.Attribute{
				"policy": enumAttribute("Image pull policy. The platform default is IF_NOT_PRESENT. ALWAYS reauthorizes pull credentials on every start.", sandboxv1.SandboxImagePullPolicy_name),
			}),
			"data_plane": dataPlaneAttribute(),
		},
	}
}

func runnerOverridesAttribute() schema.SingleNestedAttribute {
	env := optionalMap("Runner environment overrides. Values are stored in Terraform state; use a protected state backend.")
	env.Sensitive = true
	return optionalObject("Customer-settable runner deployment overrides.", map[string]schema.Attribute{
		"node_selector": optionalMap("Node label selectors for the runner deployment."),
		"tolerations": optionalObjects("Kubernetes tolerations for runner pods.", map[string]schema.Attribute{
			"key":      optionalString("Taint key."),
			"operator": schema.StringAttribute{Optional: true, MarkdownDescription: "Toleration operator (`Equal` or `Exists`).", Validators: []validator.String{stringvalidator.OneOf("Equal", "Exists")}},
			"value":    optionalString("Taint value."),
			"effect":   schema.StringAttribute{Optional: true, MarkdownDescription: "Taint effect. Empty matches all effects.", Validators: []validator.String{stringvalidator.OneOf("", "NoSchedule", "PreferNoSchedule", "NoExecute")}},
		}),
		"resources": optionalObject("CPU and memory requests and limits for runner pods, as Kubernetes quantities.", map[string]schema.Attribute{
			"cpu_request": optionalString("CPU request."), "memory_request": optionalString("Memory request."),
			"cpu_limit": optionalString("CPU limit."), "memory_limit": optionalString("Memory limit."),
		}),
		"annotations": optionalMap("Annotations on runner pods."),
		"labels":      optionalMap("Labels on runner pods."),
		"env":         env,
		"args":        schema.ListAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Ordered runner command-line arguments."},
		"scaling": optionalObject("Runner replica and autoscaling configuration.", map[string]schema.Attribute{
			"replicas":            optionalInt("Desired replicas when autoscaling is disabled."),
			"autoscaling_enabled": optionalBool("Enable runner autoscaling."),
			"min_replicas":        optionalInt("Minimum replicas."),
			"max_replicas":        optionalInt("Maximum replicas."),
		}),
		"cpu_runtime_class": optionalString("Runtime class for CPU sandboxes."),
		"gpu_runtime_class": optionalString("Runtime class for GPU sandboxes."),
	})
}

func dataPlaneAttribute() schema.SingleNestedAttribute {
	// Empty nested schemas are invalid in Terraform; represent the empty proto
	// message as a true-only bool and translate it at the API boundary.
	return optionalObject("Direct sandbox data endpoint. Select exactly one of disabled, cluster_ip, load_balancer, or custom. Certificates are managed by the platform.", map[string]schema.Attribute{
		"disabled": schema.BoolAttribute{Optional: true, MarkdownDescription: "Set to true to explicitly disable the direct endpoint.", Validators: []validator.Bool{trueValidator{}}},
		"cluster_ip": optionalObject("Expose through a ClusterIP service.", map[string]schema.Attribute{
			"annotations": optionalMap("Service annotations."),
		}),
		"load_balancer": optionalObject("Expose through an L4 load balancer forwarding mutual TLS to the runner.", map[string]schema.Attribute{
			"scope":                   enumAttribute("Load balancer scope.", sandboxv1.RunnerDataPlaneServiceScope_name),
			"load_balancer_class":     optionalString("Kubernetes load balancer class."),
			"annotations":             optionalMap("Service annotations."),
			"source_ranges":           optionalStrings("Allowed client source CIDRs."),
			"external_traffic_policy": enumAttribute("External traffic policy.", sandboxv1.RunnerDataPlaneExternalTrafficPolicy_name),
			"hostname":                optionalString("Endpoint DNS name included in the server certificate. The platform may assign one when empty."),
		}),
		"custom": optionalObject("Advertise a customer-managed network path.", map[string]schema.Attribute{
			"advertised_uri":      schema.StringAttribute{Required: true, MarkdownDescription: "Stable absolute URI returned to direct clients."},
			"service_annotations": optionalMap("Annotations on the backing ClusterIP service."),
		}),
	})
}

type trueValidator struct{}

func (trueValidator) Description(context.Context) string               { return "must be true when set" }
func (v trueValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (trueValidator) ValidateBool(_ context.Context, req validator.BoolRequest, resp *validator.BoolResponse) {
	if !req.ConfigValue.IsNull() && !req.ConfigValue.IsUnknown() && !req.ConfigValue.ValueBool() {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid selection", "Set this attribute to true to select it, or omit it to select another mode.")
	}
}

// canonicalCIDRValidator avoids server normalization changing configured values
// after apply. Sets handle server sorting and deduplication independently.
type canonicalCIDRValidator struct{}

func (canonicalCIDRValidator) Description(context.Context) string {
	return "must be a canonical IPv4 or IPv6 network prefix"
}
func (v canonicalCIDRValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (canonicalCIDRValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	prefix, err := netip.ParsePrefix(req.ConfigValue.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR", "Expected an IPv4 or IPv6 CIDR network prefix.")
		return
	}
	if prefix.Addr().Is4In6() {
		resp.Diagnostics.AddAttributeError(req.Path, "IPv4-mapped CIDR", "Use an ordinary IPv4 CIDR instead of an IPv4-mapped IPv6 prefix.")
		return
	}
	if canonical := prefix.Masked().String(); canonical != req.ConfigValue.ValueString() {
		resp.Diagnostics.AddAttributeError(req.Path, "Non-canonical CIDR", fmt.Sprintf("Use the canonical network prefix %q.", canonical))
	}
}

func policyAttribute() schema.SingleNestedAttribute {
	base := schema.StringAttribute{Optional: true, CustomType: jsontypes.NormalizedType{}, Sensitive: true, MarkdownDescription: "Runtime defaults and attachments as a JSON object, for example `jsonencode({ ... })`. Typed constraints belong in constraints. Values are stored in Terraform state."}
	cidrs := optionalStrings("Canonical, nonredundant IPv4/IPv6 prefixes (at most 256). Do not include a prefix already covered by another entry. An empty set denies every public source. Omitting the enclosing source_ip_allowlist leaves access unrestricted.")
	cidrs.Validators = []validator.Set{setvalidator.SizeAtMost(256), setvalidator.ValueStringsAre(canonicalCIDRValidator{})}
	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: "Policy governing every sandbox on this runner. Required even for the explicit empty posture `policy = {}`. Policy is updated with an etag to detect concurrent changes.",
		Attributes: map[string]schema.Attribute{
			"display_name": optionalString("Human-readable policy name."),
			"base":         base,
			"constraints":  policyConstraintsAttribute(),
			"control_plane_access": optionalObject("Request-time controls in addition to authentication and tenant authorization.", map[string]schema.Attribute{
				"source_ip_allowlist": optionalObject("Public source IP restriction. A present empty object denies every public source; an absent object imposes no source IP restriction.", map[string]schema.Attribute{"cidrs": cidrs}),
			}),
		},
	}
}

func policyConstraintsAttribute() schema.SingleNestedAttribute {
	return optionalObject("Typed constraints applied to the resolved sandbox specification. Omitted groups impose no constraint of that kind; individual security allowlists remain fail-closed.", map[string]schema.Attribute{
		"resources": optionalObject("Per-container bounds, defaults, and whole-sandbox ceilings.", map[string]schema.Attribute{
			"max_cpu":        optionalString("Maximum CPU per container, as a Kubernetes quantity."),
			"max_memory":     optionalString("Maximum memory per container."),
			"min_cpu":        optionalString("Minimum CPU per container."),
			"min_memory":     optionalString("Minimum memory per container."),
			"default_cpu":    optionalString("Default CPU per container."),
			"default_memory": optionalString("Default memory per container."),
			"max_gpu_count":  optionalInt("Maximum GPUs per container. Omitted means no cap; explicit zero forbids GPUs."),
			"cpu_ceiling":    optionalString("Sum-of-requests CPU ceiling across the sandbox."),
			"memory_ceiling": optionalString("Sum-of-requests memory ceiling across the sandbox."),
			"require_limits": optionalBool("Require every container to declare requests and limits."),
		}),
		"image": optionalObject("Image restrictions.", map[string]schema.Attribute{
			"allowed_registries": optionalStrings("Allowed registry prefixes. Empty permits any registry."),
			"allowed_images":     optionalStrings("Allowed image references. Empty permits any image within the registry restrictions."),
		}),
		"network": optionalObject("Network envelopes and defaults. Empty allowed envelopes impose no restriction.", map[string]schema.Attribute{
			"allowed_egress":  networkRulesAttribute(true, "Allowed egress envelope. DNS wildcard `*` is permitted here."),
			"default_egress":  networkRulesAttribute(true, "Egress applied when the sandbox specifies none. DNS-name destinations are not permitted here."),
			"deny_dns":        optionalBool("Forbid DNS-name egress grants. Does not disable DNS resolution."),
			"allowed_ingress": networkRulesAttribute(false, "Allowed ingress sources for custom-visibility ports."),
			"default_ingress": networkRulesAttribute(false, "Ingress applied when the sandbox specifies none."),
		}),
		"security": optionalObject("Container privilege and runtime-class constraints.", map[string]schema.Attribute{
			"allow_privileged":          optionalBool("Permit privileged containers."),
			"allowed_capabilities":      optionalStrings("Linux capabilities containers may add. Empty permits none beyond defaults."),
			"allowed_seccomp_profiles":  optionalStrings("Permitted seccomp profiles, such as RuntimeDefault or Unconfined."),
			"allowed_runtime_classes":   optionalStrings("Runtime classes callers may explicitly select. Empty forbids caller-selected runtime classes."),
			"default_cpu_runtime_class": optionalString("Default CPU runtime class. Must fit a nonempty runtime-class allowlist."),
			"default_gpu_runtime_class": optionalString("Default GPU runtime class. Must fit a nonempty runtime-class allowlist."),
		}),
		"instance":  optionalObject("Node instance-type restrictions.", map[string]schema.Attribute{"allowed_instance_types": optionalStrings("Allowed instance types. Empty permits any offered by the runner.")}),
		"lifecycle": optionalObject("Lifetime defaults.", map[string]schema.Attribute{"default_lifetime_seconds": schema.Int64Attribute{Optional: true, MarkdownDescription: "Default sandbox lifetime in seconds; zero leaves the platform default. Maximum 30 days.", Validators: []validator.Int64{int64validator.Between(0, 2592000)}}}),
		"metadata": optionalObject("Annotation restrictions.", map[string]schema.Attribute{
			"denied_annotation_prefixes": optionalStrings("Rejected annotation key prefixes."),
			"max_annotation_count":       optionalInt("Maximum annotations; zero means no limit."),
		}),
		"volumes": optionalObject("Sandbox volume restrictions.", map[string]schema.Attribute{
			"allowed_media": schema.SetAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Allowed storage media. Empty permits any.", Validators: []validator.Set{setvalidator.ValueStringsAre(stringvalidator.OneOf(coreweave.EnumValues(sandboxv1.StorageMedium_name, true)...))}},
			"max_size":      optionalString("Maximum size per volume, for example 100Gi."),
		}),
	})
}

func networkRulesAttribute(egress bool, description string) schema.ListNestedAttribute {
	attributes := map[string]schema.Attribute{
		"cidr": optionalObject("CIDR range with optional carve-outs.", map[string]schema.Attribute{
			"cidr":   schema.StringAttribute{Required: true, MarkdownDescription: "IPv4 or IPv6 CIDR.", Validators: []validator.String{canonicalCIDRValidator{}}},
			"except": optionalStrings("Excluded subranges."),
		}),
		"tenant": enumAttribute("Relational tenant selection.", sandboxv1.TenantScope_name),
		"any":    schema.BoolAttribute{Optional: true, MarkdownDescription: "Set to true to select any address-shaped peer.", Validators: []validator.Bool{trueValidator{}}},
		"ports": optionalObjects("Allowed ports. Empty means all ports, except DNS destinations which use HTTPS (TCP 443).", map[string]schema.Attribute{
			"protocol": schema.StringAttribute{Optional: true, MarkdownDescription: "TCP (default), UDP, or SCTP.", Validators: []validator.String{stringvalidator.OneOf("TCP", "UDP", "SCTP")}},
			"port":     schema.Int64Attribute{Required: true, MarkdownDescription: "Starting port.", Validators: []validator.Int64{int64validator.Between(1, 65535)}},
			"end_port": schema.Int64Attribute{Optional: true, MarkdownDescription: "Inclusive end port; zero or omitted means a single port.", Validators: []validator.Int64{int64validator.Between(0, 65535)}},
		}),
	}
	if egress {
		attributes["dns_name"] = optionalString("Exact DNS name or a single leftmost wildcard label; `*` is allowed only in allowed_egress.")
		attributes["dns_name_except"] = optionalStrings("DNS names excluded from an allowed_egress DNS envelope.")
		attributes["selector"] = optionalObject("Explicit entitlement to reach cluster workloads by label.", map[string]schema.Attribute{
			"pod_labels":       schema.MapAttribute{Required: true, ElementType: types.StringType, MarkdownDescription: "Pod labels to match (at least one required)."},
			"namespace_labels": optionalMap("Namespace labels to match. Omitted means the sandbox's own namespace."),
		})
	}
	return optionalObjects(description+" Select exactly one peer kind per rule.", attributes)
}
