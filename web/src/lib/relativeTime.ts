import dayjs from 'dayjs';
import dayjsDuration from 'dayjs/plugin/duration';
import dayJsrelativeTime from 'dayjs/plugin/relativeTime';
import dayjsUpdateLocale from 'dayjs/plugin/updateLocale';
dayjs.extend(dayJsrelativeTime);
dayjs.extend(dayjsDuration);
dayjs.extend(dayjsUpdateLocale);
dayjs.updateLocale('en', {
  relativeTime: {
    future: 'in %s',
    past: '%s ago',
    s: '%d seconds',
    m: 'a minute',
    mm: '%d minutes',
    h: 'an hour',
    hh: '%d hours',
    d: 'a day',
    dd: '%d days',
    M: 'a month',
    MM: '%d months',
    y: 'a year',
    yy: '%d years',
  },
});

export function relativeTime(to: Date, from: Date = new Date()) {
  if (!to) return null;

  if (to.getTime() < from.getTime()) {
    return dayjs(to).from(from);
  } else {
    return dayjs(from).to(to);
  }
}

export function humanizeDuration(duration: number, unit: dayjsDuration.DurationUnitType = 'seconds') {
  return dayjs.duration(duration, unit).humanize();
}
