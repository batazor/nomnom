import type { SupportedLocale } from "../../shared/config/runtime-config";

// Names of cooks and couriers added on the board, made up when one is added: a
// first name and the initial of a last name ("Анна К."). A name already in the
// pool is skipped while another is free; the pool may repeat names only once
// all of them are taken, since workers are told apart by id.
const firstNames: Record<SupportedLocale, readonly string[]> = {
  ru: [
    "Анна",
    "Борис",
    "Вера",
    "Глеб",
    "Дарья",
    "Егор",
    "Жанна",
    "Илья",
    "Карина",
    "Лев",
    "Мария",
    "Никита",
    "Ольга",
    "Павел",
    "Роман",
    "Светлана",
    "Тимур",
    "Ульяна",
    "Фёдор",
    "Юлия",
  ],
  en: [
    "Alex",
    "Bella",
    "Chris",
    "Dana",
    "Eli",
    "Fiona",
    "George",
    "Hannah",
    "Ivan",
    "Julia",
    "Kevin",
    "Laura",
    "Mark",
    "Nina",
    "Oscar",
    "Paula",
    "Ryan",
    "Sara",
    "Tom",
    "Vera",
  ],
};

const lastInitials: Record<SupportedLocale, string> = {
  ru: "АБВГДЕЗИКЛМНОПРСТФЧШ",
  en: "ABCDEFGHJKLMNPRSTVWY",
};

export function newWorkerName(
  locale: SupportedLocale,
  taken: readonly string[],
  random: () => number = Math.random,
): string {
  const all = firstNames[locale].flatMap((first) =>
    [...lastInitials[locale]].map((initial) => `${first} ${initial}.`),
  );
  const takenNames = new Set(taken);
  const free = all.filter((name) => !takenNames.has(name));
  const pool = free.length > 0 ? free : all;
  return pool[Math.floor(random() * pool.length)];
}
