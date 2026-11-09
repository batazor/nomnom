//! Delivery Duration Estimate (FR-676, FR-691), секция `spec.deliveryEstimate`:
//! размер выборки Delivery Duration Statistics и базовые длительности
//! `courier_lead` и `transit` до её набора. Значений по умолчанию нет:
//! отсутствующее или нечисловое значение завершает startup. Положительность
//! проверяет сама политика оценки (`DeliveryEstimatePolicy`) при сборке
//! процесса — тоже до открытия портов.

use std::time::Duration;

use serde::Deserialize;

pub const DELIVERY_ESTIMATE_SAMPLE_SIZE_KEY: &str = "spec.deliveryEstimate.sampleSize";
pub const DELIVERY_ESTIMATE_BASELINE_COURIER_LEAD_KEY: &str =
    "spec.deliveryEstimate.baselineCourierLead";
pub const DELIVERY_ESTIMATE_BASELINE_TRANSIT_KEY: &str = "spec.deliveryEstimate.baselineTransit";

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct DeliveryEstimateConfig {
    /// Длительностей каждого вида на Market в выборке.
    pub sample_size: u32,
    /// Create Delivery → Pickup, пока выборка не набрана.
    #[serde(with = "humantime_serde")]
    pub baseline_courier_lead: Duration,
    /// Pickup → вручение, пока выборка не набрана.
    #[serde(with = "humantime_serde")]
    pub baseline_transit: Duration,
}
