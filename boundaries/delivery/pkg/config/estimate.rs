//! Delivery Duration Estimate (FR-676, FR-691): размер выборки Delivery
//! Duration Statistics и базовые длительности `courier_lead` и `transit` до её
//! набора. Значений по умолчанию нет: пустое или нечисловое значение
//! завершает startup. Положительность проверяет сама политика оценки
//! (`DeliveryEstimatePolicy`) при сборке процесса — тоже до открытия портов.

use std::time::Duration;

use serde::Deserialize;

pub const DELIVERY_ESTIMATE_SAMPLE_SIZE_ENV: &str = "DELIVERY_ESTIMATE__SAMPLE_SIZE";
pub const DELIVERY_ESTIMATE_BASELINE_COURIER_LEAD_ENV: &str =
    "DELIVERY_ESTIMATE__BASELINE_COURIER_LEAD";
pub const DELIVERY_ESTIMATE_BASELINE_TRANSIT_ENV: &str = "DELIVERY_ESTIMATE__BASELINE_TRANSIT";

/// Ключ конфигурации и переменная окружения; значения по умолчанию нет.
pub(super) const DELIVERY_ESTIMATE_SETTINGS: [(&str, &str); 3] = [
    (
        "delivery_estimate.sample_size",
        DELIVERY_ESTIMATE_SAMPLE_SIZE_ENV,
    ),
    (
        "delivery_estimate.baseline_courier_lead",
        DELIVERY_ESTIMATE_BASELINE_COURIER_LEAD_ENV,
    ),
    (
        "delivery_estimate.baseline_transit",
        DELIVERY_ESTIMATE_BASELINE_TRANSIT_ENV,
    ),
];

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
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
