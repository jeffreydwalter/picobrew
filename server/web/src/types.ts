export interface Session {
  authenticated: boolean
  setup_required: boolean
  csrf_token: string
  version: string
  demo: boolean
}
export interface DeviceStatus {
  maintenance?: { available?: boolean; due: string[] }
  state?: string
  temperature_f?: number | null
  pressure_kpa?: number | null
  battery_v?: number | null
  sensors?: Sensor[]
  actuators?: Actuator[]
  recovery?: InterruptedRecovery | null
  model?: string
  model_name?: string
  model_family?: string
  display_variant?: string
  uptime_ms: number
  ready: boolean
  device_uid: string
  stock_serial?: string
  busy: boolean
  stopped: boolean
  command: string
  device_state: number
  phase: number
  controller_status: number
  compartment: number
  wort_f: number | null
  thermoblock_f: number | null
  target_f: number | null
  step_seconds: number
  current_step: number
  step_count: number
  recipe_name: string
  step_name: string
  session_id: string
  filter_inserted: boolean | null
  stop_reason: string
  encoder: { position: number; min: number; max: number }
}
export interface InterruptedRecovery {
  available: boolean
  valid: boolean
  recipe_name: string
  step_name: string
  current_step: number
  step_seconds: number
  elapsed_seconds: number
}
export interface Sensor {
  id: string
  label: string
  kind: string
  unit: string
  value: number | null
}
export interface Actuator {
  id: string
  label: string
  kind: string
  enabled: boolean | null
  level: number | null
  rpm: number | null
}
export interface FieldMetadata {
  label: string
  unit: string
  kind: string
}
export interface Capabilities {
  events_persistent?: boolean
  history_sample_interval_ms?: number
  pairing_v1?: boolean
  picostill_brewer_link_v1?: boolean
  capabilities?: Record<string, boolean>
  settings_fields?: string[] | null
  command_fields?: Record<string, { allowed: string[]; required: string[] }>
  model?: string
  model_name?: string
  model_family?: string
  display_variant?: string
  api_version: number
  release_channel?: string
  firmware_version: string
  commands: string[]
  recipe_capacity: number
  step_capacity: number
  front_panel: boolean
  history_persistent: boolean
  telemetry_forwarding: boolean
  dedicated_pause_resume: boolean
  z_program_maker?: boolean
  z_cloud_program_v1?: boolean
  recipe_create_only?: boolean
  zero_minute_steps?: boolean
}
export interface Device {
  id: string
  brewer_id?: string
  name: string
  model: string
  model_source?: 'pending' | 'firmware' | 'manual'
  model_error?: string
  model_error_code?: string
  model_selection_required?: boolean
  adapter: string
  base_url: string
  location: string
  enabled: boolean
  key_configured: boolean
  key_reused?: boolean
  created_at: string
  online: boolean
  last_seen: string | null
  error?: string
  status: DeviceStatus | null
  capabilities: Capabilities | null
  telemetry?: {
    state: string
    enabled?: boolean
    message?: string
    host?: string
    port?: number
    last_checked?: string
  }
}
export interface Display {
  dial?: 'menu' | 'press' | 'off'
  offset?: number
  total_bytes?: number
  next_offset?: number | null
  revision?: number
  width: number
  height: number
  rotation?: number
  display_variant?: string
  storage_bytes?: number
  transfer_bytes?: number
  format: string
  data: string
}
export interface MachineStep {
  name: string
  location: number
  target_f: number
  minutes: number
  drain_minutes: number
}
export type ZProgramStep =
  | {
      Name: string
      StepType: 1
      Location: number
      Temp: number | null
      Time: number
      Drain: number
    }
  | { Name: string; StepType: 2; Time?: number }
export interface ZProgram {
  IsMetric?: boolean
  Name: string
  IsBeer: boolean
  UsePicoPakMode: boolean
  Steps: ZProgramStep[]
}
export interface ZPicoPak {
  recipe_id: number
  beer_id: number
  abv: number
  ibu: number
  selected_abv?: number
  selected_ibu?: number
  step_options: { action: number; min_time?: number; max_time?: number }[]
}
export interface MachineRecipe {
  z_pak?: ZPicoPak
  z_program?: ZProgram
  z_cloud?: { start_water_ml: number; is_beer: boolean }
  id: string
  name: string
  steps: MachineStep[]
}
export interface Ingredient {
  slot_addition?: boolean
  name: string
  kind: string
  amount: number
  unit: string
  use: string
  minutes: number
  alpha?: number
  type?: string
  form?: string
  attenuation?: number
  yield?: number
  color?: number
}
export interface RecipeStep {
  name: string
  kind: string
  target_c: number
  minutes: number
  drain_minutes: number
  location: number
  type?: string
  infuse_liters?: number
  ramp_minutes?: number
}
export interface RecipeProvenance {
  catalog_id: string
  source_id: string
  repository: string
  commit: string
  path: string
  sha256: string
  source_url: string
  license: string
  warnings: string[]
}
export interface Recipe {
  source_yeast?: string
  machine_versions?: Adapted[]
  id: string
  name: string
  style: string
  description: string
  estimated_yield?: boolean
  batch_liters: number
  efficiency: number
  ingredients: Ingredient[]
  steps: RecipeStep[]
  tags: string[]
  notes: string
  revision: number
  created_at?: string
  updated_at?: string
  type?: string
  boil_liters?: number
  boil_minutes?: number
  water?: Record<string, number>
  fermentation?: { name: string; target_c: number; days: number }[]
  carbonation?: { method?: string; volumes?: number; temperature_c?: number }
  kind?: string
  source_model?: string
  native_program?: MachineRecipe
  provenance?: RecipeProvenance
}
export interface Profile {
  id: string
  name: string
  batch_liters: number
  efficiency: number
  max_steps: number
  max_target_c: number
  max_grain_kg: number
  max_hop_slot_g?: number
  hop_slots: number
  control_supported: boolean
  notes: string
  sources?: { label: string; url: string; description: string }[]
  user_configured?: boolean
}
export interface Adapted {
  hop_slots_by_minute?: Record<number, number>
  recipe: Recipe
  machine_recipe: MachineRecipe | null
  profile: Profile
  warnings: string[]
  deployment_blockers?: string[]
  can_deploy: boolean
  source_revision: number
  profile_fingerprint?: string
}
export interface Sample {
  zymatic_history?: boolean
  temperature_f?: number | null
  pressure_kpa?: number | null
  battery_v?: number | null
  readings?: Record<string, number | null>
  elapsed_ms: number
  observed_at?: string
  wort_f: number | null
  thermoblock_f: number | null
  target_f: number | null
  step_seconds?: number
  phase?: number
  controller_status?: number
  compartment: number | null
  current_step: number | null
  flags?: number
  device_state?: number
  source: string
}
export interface Conditioning {
  revision: number
  recipe_name?: string
  recipe_revision?: number
  stages: {
    name: string
    target_c?: number
    days?: number
    volumes?: number
    started_at?: string
    completed_at?: string
  }[]
}
export interface Brew {
  group_id?: string
  group_name?: string
  group_session_id?: string
  group_source_device_id?: string
  picoferm_id?: string
  picoferm_name?: string
  picoferm_recording_id?: string
  recovered_after_restart?: boolean
  conditioning?: Conditioning
  step_names?: Record<number, string>
  sample_fields?: string[]
  sample_interval_ms?: number
  field_metadata?: Record<string, FieldMetadata>
  id: string
  device_id: string
  device_name: string
  model: string
  recipe_name: string
  recipe_id?: string
  recipe_revision?: number
  state: string
  command: string
  started_at: string | null
  observed_at: string
  duration_ms: number
  device_history_id: number
  gap_count: number
  truncated: boolean
  notes: string
  rating: number
  sample_count: number
  source: string
}
export interface BrewDetail extends Brew {
  samples: Sample[]
  ai_advice?: AIAdvice[]
}
export interface AIAdvice {
  id: string
  created_at: string
  device_id?: string
  brew_id?: string
  mode: string
  status: string
  text: string
  recipe?: Recipe
  limitations?: string[]
  error?: string
}
export interface DeviceSettings {
  auto_cycle?: boolean
  temperature_unit: 'F' | 'C'
  language: string
  detailed_status: boolean
}
export interface Telemetry {
  enabled: boolean
  host: string
  port: number
  interval_ms?: number
  path?: string
  state?: string
  message?: string
  last_checked?: string
  status?: { state: string; sent: number; failed: number; dropped: number }
}
export interface FirmwareSettings {
  repository: string
  ref: string
}
export interface FirmwareCandidate {
  ota_protocol?: string
  hub_ota_protocol?: number
  model: string
  model_family: string
  display_variant?: string
  firmware_version: string
  binary_sha256: string
  binary_bytes: number
}
export interface FirmwareRelease extends FirmwareCandidate {
  archive_sha256: string
  kind: 'historical' | 'standalone' | 'reference' | 'picoferm-ab'
}
export interface FirmwareUpdates {
  checked?: string
  releases: FirmwareRelease[]
  notices: { device_id: string; name: string; version: string; revised?: boolean }[]
}
export interface FirmwareCatalog extends FirmwareSettings {
  commit: string
  packages: FirmwareCandidate[]
}
export interface FirmwarePackage extends FirmwareCandidate {
  id: string
  package_bytes: number
  created_at: string
  validation: 'passed'
  provenance: {
    source: 'upload' | 'github' | 'library'
    repository?: string
    commit?: string
  }
}
export interface MachineFirmware {
  can_confirm_stock_startup?: boolean
  stock_restore_wireless?: boolean
  stock_restore_upgrade?: boolean
  last_stock_restore?: {
    started: string
    device_uid: string
    model: string
    stage: string
    state: string
    message: string
  }
  stock_restore_guide?: boolean
  usb_restore_target?: { model: string; device_uid: string }
  device_id: string
  model: string
  current_version: string
  display_variant?: string
  verified_display_variant?: string
  device_uid?: string
  hardware_verification_required?: boolean
  retry_package_id?: string
  revised_package_id?: string
  install_supported: boolean
  last_install?: { package_id: string; started: string; state: string; message: string }
  reason: string
  packages: FirmwarePackage[]
}
export type AIAuthMode = 'api_key' | 'subscription'
export interface AISettings {
  provider: string
  auth_mode?: AIAuthMode
  model: string
  key_configured: boolean
  enabled: boolean
  monitor_enabled?: boolean
}
export interface AIModel {
  id: string
  name: string
}
export interface AIModels {
  provider: string
  auth_mode?: AIAuthMode
  models: AIModel[]
}
export interface AISubscription {
  provider: string
  installed: boolean
  authenticated: boolean
  message: string
  login_command: string
  pending?: boolean
  callback_url?: string
  authorization_url?: string
  account?: string
}
export interface AIResult {
  text: string
  recipe?: Recipe
  limitations: string[]
}
export type Unit = 'F' | 'C'

export interface CatalogSource {
  id: string
  name: string
  repository: string
  upstream: string
  commit: string
  license: string
  attribution: string
  notes: string
  record_count: number
  valid_count: number
  entry_count: number
  excluded_count: number
  excluded_reasons: Record<string, number>
  asset_count: number
  reference_count: number
}
export interface CatalogSummary {
  edition?: string
  brew_ready?: boolean
  id: string
  name: string
  kind: string
  source_id: string
  source_model: string
  models: string[]
  deploy_models: string[]
  style: string
  author: string
  description: string
  abv?: number
  ibu?: number
  srm?: number
  image_url?: string
  image_kind?: string
  source_record_count: number
  can_copy: boolean
  can_deploy: boolean
  featured: boolean
  official: boolean
}
export interface CatalogDetail extends CatalogSummary {
  source_review?: { evidence: string; blocker?: string }
  machine_versions?: Adapted[]
  brew_sheet?: {
    instructions: string[]
    yeast_guide: string
    original_gravity?: number
    final_gravity?: number
  }
  stock_program?: MachineRecipe
  marketplace?: {
    creator?: string
    ingredient_recipe_id?: string
    pico_beer_id: number
    name: string
    url: string
    style?: string
    grains?: string
    hops?: string
  }
  recipe?: Recipe
  native_program?: MachineRecipe
  warnings: string[]
  provenance: RecipeProvenance
  source_records: { id: string; path: string; sha256: string; bytes: number }[]
  source_url: string
  notes: string
  source_parameters?: unknown
}
export interface CatalogPage {
  items: CatalogSummary[]
  total: number
  offset: number
  limit: number
  sources: CatalogSource[]
  styles: string[]
}
export interface CatalogReferences {
  items: {
    id: string
    title: string
    description: string
    models: string[]
    source_id: string
    asset_path: string
    url: string
    content_type: string
    source_url: string
    bundled: boolean
    available: boolean
    download_url?: string
    sha256: string
  }[]
  assets: {
    path: string
    url: string
    title: string
    kind: string
    models: string[]
    source_id: string
    source_path: string
    sha256: string
    bytes: number
    content_type: string
    attribution: string
    license: string
    bundled: boolean
    download_url?: string
  }[]
}
export interface DeploymentItem {
  hop_slots_by_minute?: Record<number, number>
  recipe_id: string
  source_revision: number
  batch_liters: number
  name: string
  status: string
  reason?: string
  update: boolean
  warnings: string[]
  native?: MachineRecipe
  previous?: MachineRecipe
}
export interface BulkDeployment {
  id: string
  device_id: string
  device_uid: string
  profile_id: string
  state: string
  created_at: string
  expires_at: string
  capacity: number
  used: number
  available: number
  planned_count: number
  new_count: number
  update_count: number
  verified_count: number
  started: boolean
  inventory_sha256: string
  items: DeploymentItem[]
}
