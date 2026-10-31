/** Application clock; domain code receives time only as explicit values. */
export interface Clock {
  now(): Date;
}
