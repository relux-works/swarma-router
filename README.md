# swarma-router

Chooses who runs a task and on what model. The router takes a board task together with the member's card and the
worker's current state: agent, context, limits. A small, fast local classifier weighs the available vendors and models,
the usage thresholds, the schedule of desired runs and the preferences by project and work type. It knows that a model
or vendor switch resets the context, and it recommends whether to keep the worker as it is or move it to a simpler or
a stronger model, with lower or higher reasoning. Managers assign tasks through it and adapt their workers at runtime.

It is an independent module. It connects to the model catalog, the session host's usage events, the board and the
product cards through plugin seams, not hard wiring.

Status: preparation. The concept comes first.

Owner direction, 2026-10-30: «да, нам нужен будет еще модуль который будет понимать чо за задача и какие модели каких
вендоров доступны / так же будет иметь расписание желаемых запусков по времени - скедулинг / возможно предпочтения по
проектам и типам работ на кого спавнить , а также какие пороги / аля сварма роутер / надо на эту тему бук свармы
полистать / им будет пользоваться руководитель чтоб адаптировать своих работников прям рантайм если надо / типо
переключить на модель попроще или наоборот пожирней и ризонинг поднять / это уже более серьезное решение будет чем были
спавны в борде / здесь будет локальная модель какие там щас шустрые и пиздатые модели-классификаторы  желательно не
сильно жирные) / т.е в него будет вгружаться задача со всем чо есть типо какой щас агент, какой у него контекст , а
классификатор будет смареть на ограничения свои , доступные модели, втыкать что пнреключение модели - сброс контекста
равно как и смена вендора и принимать взвешенное решение чо делать с этим воркером менять или оставлять / и так
руководитель будет раскидывать задачи на работяжек через роутер с аздачей с борды и идентикой-карточкой сотрудника )».
