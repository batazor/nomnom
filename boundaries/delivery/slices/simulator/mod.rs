//! Слайс `simulator`: демо Simulator курьеров (FR-604, FR-605). Слайс
//! владеет переключателем Simulator и выборкой работы для него; действия
//! курьеров выполняют настоящие use cases слайса `delivery`, их вызывает
//! фоновый цикл корневого `di` (`di::SimulatorWorker`).

pub mod applications;
pub mod di;
pub mod domains;
