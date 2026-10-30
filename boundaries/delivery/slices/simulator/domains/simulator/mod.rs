//! Состояние Simulator процесса: одна запись в хранилище сервиса, переживает
//! рестарт; отсутствие записи — Simulator выключен (FR-604).

use crate::pkg::persistence::RepositoryError;

/// Хранилище переключателя Simulator.
pub trait SimulatorStateRepository {
    /// `false`, пока Simulator ни разу не включали.
    fn enabled(&self) -> Result<bool, RepositoryError>;

    fn set_enabled(&self, enabled: bool) -> Result<(), RepositoryError>;
}
