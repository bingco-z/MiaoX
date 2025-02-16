package MiaoX

import (
	"errors"
	"github.com/bincooo/MiaoX/types"
	"github.com/bincooo/MiaoX/vars"
	"github.com/sirupsen/logrus"
	"sync"
	"time"
)

type ConversationStack struct {
	conversation types.ConversationContext
	handle       func(types.PartialResponse)
}

// 通用限流器
// 内置分组限流和全局限流
type CommonLimiter struct {
	lmt  *Limiter
	gLmt *GroupLimiter
}

// 全局限流器
type Limiter struct {
	sync.RWMutex

	max   int                 // 最大队列数量
	stack []ConversationStack // 会话栈存放列表
	mgr   types.BotManager    // AI管理器
}

// 群组限流器
type GroupLimiter struct {
	kv    map[string]*Limiter
	chain map[string]types.Interceptor
}

func NewCommonLimiter() *CommonLimiter {
	return &CommonLimiter{
		lmt:  NewLimiter(),
		gLmt: NewGroupLimiter(),
	}
}

func NewLimiter() *Limiter {
	lmt := Limiter{
		max:   3,
		stack: []ConversationStack{},
		mgr:   NewBotManager(),
	}
	go lmt.Run()
	return &lmt
}

func NewGroupLimiter() *GroupLimiter {
	lmt := GroupLimiter{
		kv:    make(map[string]*Limiter),
		chain: make(map[string]types.Interceptor),
	}
	return &lmt
}

func (cLmt *CommonLimiter) Join(context types.ConversationContext, handle func(types.PartialResponse)) error {
	lmt := cLmt.matchLimiter(context.Bot)
	if lmt == nil {
		return errors.New("未知的`AI`类型")
	}
	return lmt.Join(context, handle)
}

func (cLmt *CommonLimiter) Remove(id string, bot string) {
	lmt := cLmt.matchLimiter(bot)
	if lmt != nil {
		lmt.Remove(id, bot)
	}
}

func (cLmt *CommonLimiter) RegChain(name string, inter types.Interceptor) error {
	if err := cLmt.gLmt.RegChain(name, inter); err != nil {
		return err
	}

	if err := cLmt.lmt.RegChain(name, inter); err != nil {
		return err
	}

	return nil
}

func (cLmt *CommonLimiter) matchLimiter(bot string) types.Limiter {
	switch bot {
	case vars.OpenAIAPI, vars.Claude, vars.Bing:
		return cLmt.gLmt
	case vars.OpenAIWeb:
		return cLmt.lmt
	default:
		return nil
	}
}

// ==== Limiter =====

func (lmt *Limiter) Join(context types.ConversationContext, handle func(types.PartialResponse)) error {
	lmt.Lock()
	defer lmt.Unlock()

	if len(lmt.stack) > lmt.max {
		return errors.New("忙不过来了，CPU都得冒烟~")
	}

	lmt.stack = append(lmt.stack, ConversationStack{
		conversation: context,
		handle:       handle,
	})
	logrus.Info("[MiaoX] - 已加入队列")
	return nil
}

func (lmt *Limiter) Remove(id string, bot string) {
	lmt.mgr.Remove(id, bot)
}

func (lmt *Limiter) RegChain(name string, inter types.Interceptor) error {
	return lmt.mgr.RegChain(name, inter)
}

// ==== End =====

// ==== GroupLimiter =====

func (gLmt *GroupLimiter) Join(context types.ConversationContext, handle func(types.PartialResponse)) error {
	// 群和好友各自用一个限流
	lmt, ok := gLmt.kv[context.Id]
	if !ok {
		lmt = NewLimiter()
		for name, iter := range gLmt.chain {
			if err := lmt.RegChain(name, iter); err != nil {
				return err
			}
		}
		gLmt.kv[context.Id] = lmt
	}

	return lmt.Join(context, handle)
}

func (gLmt *GroupLimiter) Remove(id string, bot string) {
	if lmt, ok := gLmt.kv[id]; ok {
		lmt.Remove(id, bot)
	}
}

func (gLmt *GroupLimiter) RegChain(name string, inter types.Interceptor) error {
	gLmt.chain[name] = inter
	return nil
}

// ==== End ====

func (lmt *Limiter) Run() {
	waitTimeout := time.Second
	for {
		if len(lmt.stack) == 0 {
			time.Sleep(waitTimeout)
			continue
		}

		lmt.Lock()
		s := lmt.stack[0]
		lmt.stack = lmt.stack[1:len(lmt.stack)]
		lmt.Unlock()

		logrus.Info("[MiaoX] - 正在应答，ID = ", s.conversation.Id, ", Bot = ", s.conversation.Bot)
		lmt.mgr.Reply(s.conversation, s.handle)
		time.Sleep(waitTimeout)
	}
}
