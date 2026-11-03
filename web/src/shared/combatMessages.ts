import type { Locale } from './types.ts';

const ru = {
  setupRequired: 'Укажите Client ID и Client Secret от Warcraft Logs API V2 в локальном .env, затем перезапустите API и обработчик заданий.',
  statsTitle: 'Характеристики участника боя',
  statsScope: 'Значения зафиксированы в выбранном бою для выбранного участника.',
  statsObserved: 'Момент снимка от начала боя',
  statsValues: 'Значения из отчёта',
  statsMissing: 'Для этого участника в выбранном бою нет доступного снимка характеристик.',
  workspace: 'Боевые отчёты', character: 'Персонаж', top: 'ПУБЛИЧНЫЕ БОЕВЫЕ ОТЧЁТЫ', title: 'События складываются в историю',
  subtitle: 'Выберите отчёт, бой и участника. Показатели связаны с окном боя и наблюдаемыми событиями.',
  input: 'Код или ссылка Warcraft Logs', placeholder: '16 символов или ссылка на отчёт', open: 'Открыть отчёт', invalid: 'Введите код из 16 букв и цифр или ссылку https://www.warcraftlogs.com/reports/…',
  demo: 'СИНТЕТИЧЕСКИЙ ПРИМЕР', demoText: 'Вымышленные отчёт, участники и события для локальной проверки. Это не боевые результаты Warcraft Logs.', demoOpen: 'Открыть демоотчёт',
  disabled: 'Источник боевых отчётов не подключён', disabledText: 'Получение публичных отчётов станет доступно после настройки источника на сервере.', connecting: 'Проверяем источник…', source: 'Источник', fixture: 'Синтетические данные', live: 'Warcraft Logs', public: 'Публичный отчёт', fetched: 'Загружено', stale: 'Проверка источника устарела', fresh: 'Данные актуальны', reload: 'Перечитать отчёт',
  emptyTitle: 'Начните с публичного отчёта', emptyText: 'Доступность персонажа в Blizzard не связывает его автоматически с участником лога. Участника выбирают по записи внутри отчёта.',
  fight: 'Бой', actor: 'Участник отчёта', chooseFight: 'Выберите бой', chooseActor: 'Выберите участника', kill: 'Победа', wipe: 'Без победы', outcomeUnknown: 'Исход неизвестен', difficulty: 'Сложность', duration: 'Полное время боя', unknown: 'Не сообщено',
  listsMissing: 'Источник не предоставил доступный список боёв или участников.', noFights: 'В отчёте нет доступных боёв.', noActors: 'В отчёте нет доступных участников.', noFightPlayers: 'В этом бою источник не отметил доступных игроков.', membershipUnknown: 'Источник не подтвердил список участников этого боя. Выбор доступен из игроков отчёта; отсутствие их событий само по себе не означает нулевую активность.', selectPrompt: 'Выберите бой и участника для расчёта.',
  loading: 'Получаем отчёт и проверяем доступность…', calculating: 'Получаем события и рассчитываем показатели…', paused: 'Задание ещё выполняется. Автоматическая проверка приостановлена.', retry: 'Проверить ещё раз', retryIn: 'Повтор через', seconds: 'с',
  unavailable: 'Отчёт не найден или недоступен для публичного просмотра.', denied: 'Доступ к отчёту не подтверждён. Ранее показанные данные скрыты.', error: 'Не удалось получить данные отчёта.', invalidRequest: 'Некорректный отчёт, бой или участник.', network: 'Нет соединения с API.',
  changed: 'Отчёт уже обновился. Перечитайте его, чтобы анализ соответствовал показанным боям и участникам.',
  method: 'Как рассчитаны показатели', methodText: 'DPS и HPS — суммы нормализованных amount для исходящих событий выбранного участника, делённые на полное время боя. Урон питомцев не присоединяется. Это собственный расчёт, без процентилей и ранга Warcraft Logs.',
  healingText: 'Эффективное и избыточное лечение учитываются отдельно. Доля избыточного лечения — его часть в сумме эффективного и избыточного. Отсутствующие величины остаются неизвестными.',
  complete: 'Окно событий получено полностью', incomplete: 'Окно событий получено не полностью', incompleteText: 'Полные суммы, DPS/HPS и счётчики не определены. Видимые события и способности описывают только полученную часть.', truncated: 'Таймлайн сокращён', truncatedText: 'В таймлайне показана ограниченная выборка. Полнота расчёта указана отдельно и не определяется длиной списка на экране.',
  actorAmountsText: 'Доступные суммы урона и лечения, DPS и HPS рассчитаны по исходящим событиям участника за весь бой. Таймлайн и способности отражают полученную часть общего потока.',
  incompleteCountsFinding: 'Полные счётчики применений, прерываний и смертей недоступны.',
  dps: 'DPS', hps: 'HPS', damage: 'Урон', healing: 'Эффективное лечение', overheal: 'Избыточное лечение', overhealPercent: 'Доля избыточного', casts: 'Применения', interrupts: 'Прерывания', deaths: 'Смерти',
  abilities: 'Наблюдаемые способности', ability: 'ID способности', noAbilities: 'Способности в полученных событиях не отмечены.', abilityNote: 'Числа относятся к полученным событиям; при неполном окне это не полная статистика боя.',
  findings: 'Наблюдения и границы оценки', info: 'Наблюдение', noRules: 'Недостаточно данных', evidence: 'События в таймлайне', noFindings: 'Нет дополнительных наблюдений.',
  timeline: 'Таймлайн участника', timelineNote: 'Время от начала боя. Исходящие события участника и события, направленные на него, показаны в порядке времени.', eventFilter: 'Тип событий', allEvents: 'Все события', timestamp: 'Время', event: 'Событие', from: 'От', to: 'Кому', amount: 'Значение', noEvents: 'В выбранном фильтре событий нет.', eventIndex: 'Событие',
  damageEvent: 'Урон', healEvent: 'Лечение', castEvent: 'Применение', interruptEvent: 'Прерывание', deathEvent: 'Смерть', applybuffEvent: 'Эффект наложен', refreshbuffEvent: 'Эффект обновлён', removebuffEvent: 'Эффект снят', unknownEvent: 'Тип неизвестен',
  incompleteFinding: 'Не всё окно боя доступно; итоговые показатели не рассчитаны.', amountsFinding: 'Некоторые величины событий не получены или непригодны для расчёта. Затронутые показатели остаются неизвестными.', rotationFinding: 'Проверенные правила ротации для этого боя не подключены.', cooldownFinding: 'Применимость и ожидаемое число cooldown не подтверждены.', buffFinding: 'Проверка времени действия эффектов недоступна для этого контекста.', deathFinding: 'В полученных событиях отмечены смерти. Сами по себе они не доказывают ошибку участника.', unknownFinding: 'Источник сообщил наблюдение без поддерживаемого описания.',
};
type Messages = { [K in keyof typeof ru]: string };
const en: Messages = {
  statsTitle: 'Fight participant stats',
  statsScope: 'Values recorded for the selected participant in this fight.',
  statsObserved: 'Snapshot time from fight start',
  statsValues: 'Values from the report',
  statsMissing: 'No stat snapshot is available for this participant in the selected fight.',
  setupRequired: 'Set the Warcraft Logs V2 Client ID and Client Secret in the local .env file, then restart the API and worker.',
  workspace: 'Combat reports', character: 'Character', top: 'PUBLIC COMBAT REPORTS', title: 'Events tell the story', subtitle: 'Choose a report, fight and participant. Every metric is tied to the fight window and observed events.',
  input: 'Warcraft Logs code or link', placeholder: '16 characters or a report link', open: 'Open report', invalid: 'Enter a 16-character alphanumeric code or https://www.warcraftlogs.com/reports/… link.',
  demo: 'SYNTHETIC EXAMPLE', demoText: 'An invented report, participants and events for local verification. These are not Warcraft Logs combat results.', demoOpen: 'Open demo report',
  disabled: 'Combat report source is not connected', disabledText: 'Public reports will be available after the source is configured on the server.', connecting: 'Checking source…', source: 'Source', fixture: 'Synthetic data', live: 'Warcraft Logs', public: 'Public report', fetched: 'Fetched', stale: 'Source verification is stale', fresh: 'Data is fresh', reload: 'Reload report',
  emptyTitle: 'Start with a public report', emptyText: 'A Blizzard character is not automatically linked to a log participant. Choose the participant from the report itself.',
  fight: 'Fight', actor: 'Report participant', chooseFight: 'Choose a fight', chooseActor: 'Choose a participant', kill: 'Kill', wipe: 'No kill', outcomeUnknown: 'Outcome unknown', difficulty: 'Difficulty', duration: 'Whole fight duration', unknown: 'Not provided',
  listsMissing: 'The source did not provide usable fight or participant lists.', noFights: 'No fights are available in this report.', noActors: 'No participants are available in this report.', noFightPlayers: 'The source did not identify available players in this fight.', membershipUnknown: 'Fight membership is unconfirmed. Players from the report can be selected; missing events alone do not establish zero activity.', selectPrompt: 'Choose a fight and participant to calculate metrics.',
  loading: 'Loading report and checking access…', calculating: 'Loading events and calculating metrics…', paused: 'The job is still running. Automatic polling is paused.', retry: 'Check again', retryIn: 'Retry in', seconds: 's',
  unavailable: 'The report was not found or is unavailable for public viewing.', denied: 'Report access is unconfirmed. Previously displayed data is hidden.', error: 'Could not load report data.', invalidRequest: 'Invalid report, fight or participant.', network: 'API connection unavailable.', changed: 'The report has changed. Reload it so the analysis matches the displayed fights and participants.',
  method: 'How metrics are calculated', methodText: 'DPS and HPS sum normalized outgoing event amounts for the selected participant, divided by the whole fight duration. Pet damage is excluded. This is our calculation, without Warcraft Logs percentiles or rankings.', healingText: 'Effective healing and overhealing are counted separately. The overhealing share is overhealing divided by effective healing plus overhealing. Missing values remain unknown.',
  complete: 'The event window is complete', incomplete: 'The event window is incomplete', incompleteText: 'Full totals, DPS/HPS and counts are unknown. Displayed events and abilities describe only the received portion.', truncated: 'Timeline shortened', truncatedText: 'The timeline is a limited selection. Calculation completeness is stated separately and is not inferred from the number of rows on screen.',
  actorAmountsText: 'Available damage and healing totals, DPS, and HPS use the participant’s outgoing events across the entire fight. The timeline and abilities show the portion of the wider stream that was received.',
  incompleteCountsFinding: 'Full cast, interrupt, and death counts are unavailable.',
  dps: 'DPS', hps: 'HPS', damage: 'Damage', healing: 'Effective healing', overheal: 'Overhealing', overhealPercent: 'Overhealing share', casts: 'Casts', interrupts: 'Interrupts', deaths: 'Deaths',
  abilities: 'Observed abilities', ability: 'Ability ID', noAbilities: 'No abilities were recorded in the received events.', abilityNote: 'Values describe received events; an incomplete window does not establish full fight statistics.',
  findings: 'Observations and evaluation limits', info: 'Observation', noRules: 'Insufficient data', evidence: 'Timeline events', noFindings: 'No additional observations.', timeline: 'Participant timeline', timelineNote: 'Time from fight start. Outgoing participant events and events targeting them are shown chronologically.', eventFilter: 'Event type', allEvents: 'All events', timestamp: 'Time', event: 'Event', from: 'From', to: 'To', amount: 'Amount', noEvents: 'No events match this filter.', eventIndex: 'Event',
  damageEvent: 'Damage', healEvent: 'Healing', castEvent: 'Cast', interruptEvent: 'Interrupt', deathEvent: 'Death', applybuffEvent: 'Buff applied', refreshbuffEvent: 'Buff refreshed', removebuffEvent: 'Buff removed', unknownEvent: 'Unknown type',
  incompleteFinding: 'The full fight window is unavailable; overall metrics were not calculated.', amountsFinding: 'Some event amounts are missing or unsuitable for calculation. Affected metrics remain unknown.', rotationFinding: 'Verified rotation rules are not connected for this fight.', cooldownFinding: 'Cooldown applicability and expected counts are unconfirmed.', buffFinding: 'Buff uptime evaluation is unavailable for this context.', deathFinding: 'Deaths were recorded in the received events. A death alone does not establish a participant error.', unknownFinding: 'The source returned an observation without a supported description.',
};
export function combatMessages(locale: Locale): Messages { return locale === 'ru-RU' ? ru : en; }
export function findingText(code: string, t: Messages, actorAmountsComplete = false): string {
  return ({ INCOMPLETE_EVENTS: actorAmountsComplete ? t.incompleteCountsFinding : t.incompleteFinding, AMOUNTS_UNAVAILABLE: t.amountsFinding, ROTATION_RULES_UNAVAILABLE: t.rotationFinding, COOLDOWN_RULES_UNAVAILABLE: t.cooldownFinding, BUFF_UPTIME_UNAVAILABLE: t.buffFinding, DEATHS_OBSERVED: t.deathFinding } as Record<string,string>)[code] ?? t.unknownFinding;
}
export function eventText(kind: string, t: Messages): string {
  return ({ damage:t.damageEvent, heal:t.healEvent, cast:t.castEvent, interrupt:t.interruptEvent, death:t.deathEvent, applybuff:t.applybuffEvent, refreshbuff:t.refreshbuffEvent, removebuff:t.removebuffEvent, unknown:t.unknownEvent } as Record<string,string>)[kind] ?? kind;
}
