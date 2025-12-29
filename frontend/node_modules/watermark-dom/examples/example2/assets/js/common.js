(function () {
    window.common = {
        //头部菜单切换
        topMenuInit: function () {
            sn && sn.pageType && sn.pageType[0] && $('#J_menuList li[data-page=' + sn.pageType[0] + ']').addClass('cur');
        },
        //左边菜单切换
        leftMenuInit: function () {
            sn && sn.pageType && sn.pageType[1] && $('#J_levelList dd[data-page=' + sn.pageType[1] + ']').addClass('cur');
            //一级菜单单击处理
            $('#J_levelList .l1-item').on('click', function () {
                $(this).find('em').toggleClass('fold');
                $(this).parent('li').find('dl').toggleClass('hide');
            })

        },
        eventsInit: function () {
            //日期控件绑定
            $('.date-selector').on('click', function () {
                var $this = $(this);
                $this.addClass('open');
            });
            $(document).on('click', function () {
                $('.date-selector.open').removeClass('open');
            });
        },
        allTabClickInit: function () {
            //类目选择category
            $('.category ul').each(function (index, item) {
                var $this = $(this);
                ($this.children().length == 2) && $this.addClass('col2');
                ($this.children().length == 3) && $this.addClass('col3');
            })
            $('.category').on('click', function (e) {
                e.stopPropagation();
                $('.category').not($(this)).removeClass('on')
                var $this = $(this);
                $this.toggleClass('on');

                $(document).one('click', function () {
                    if ($this.hasClass('on')) {
                        $this.removeClass('on');
                    }
                    //if($('.tooltip').hasClass('show')){
                    //	$('.tooltip.show').removeClass('show');
                    //}
                })
            });
            $('.category').on('click', 'li', function (e) {
                var $this = $(this);
                $this.addClass('active').siblings().removeClass('active');
                $this.parents('.category').find('span').text($this.text());
            });

            //表格的tab
            $('.topic-03 dl').on('click', 'dd', function () {
                var $this = $(this);
                $this.addClass('cur').siblings().removeClass('cur');
            })
            //标签tab
            $('.ch-list').on('click', 'li', function () {
                var $this = $(this);
                $this.addClass('cur').siblings().removeClass('cur');
            })

            //数据tab
            $('.moudle .item-list').on('click', 'li', function () {
                var $this = $(this);
                $this.addClass('cur').siblings().removeClass('cur');
            })

            //原点tab
            $('.radio-list').on('click', 'li', function () {
                var $this = $(this);
                $this.addClass('cur').siblings().removeClass('cur');
            })
        },
        countUp: function (target, startVal, endVal, decimals, duration, options) {
            var curCount = new CountUp(target, startVal, endVal, decimals, duration, options);
            curCount.start();
        },
        addKannma: function (number) {
            /*千分位处理*/
            var num = number + "";
            num = num.replace(new RegExp(",", "g"), "");
            var symble = "";

            if (num == 0) {
                return num;
            }

            if (/^([-+]).*$/.test(num)) {
                symble = num.replace(/^([-+]).*$/, "$1");
                num = num.replace(/^([-+])(.*)$/, "$2");
            }

            if (/^[0-9]+(\.[0-9]+)?$/.test(num)) {
                var num = num.replace(new RegExp("^[0]+", "g"), "");
                if (/^\./.test(num)) {
                    num = "0" + num;
                }

                var decimal = num.replace(/^[0-9]+(\.[0-9]+)?$/, "$1");
                var integer = num.replace(/^([0-9]+)(\.[0-9]+)?$/, "$1");

                var re = /(\d+)(\d{3})/;

                while (re.test(integer)) {
                    integer = integer.replace(re, "$1,$2");
                }
                return symble + integer + decimal;

            } else {
                return number;
            }
        },
        tabFn: function (option) {
            var option = $.extend({
                    wrapper: '.tab-wrapper',
                    tabClass: 'on',
                    content: '.tab-content',
                    contentClass: 'on',
                    fn: function(){

                    },
                    tabContent: '.tab-item'
                }, option),
                target,content,tabContent;

            if (typeof option.wrapper == 'object') {
                target = option.wrapper
            } else {
                target = $(option.wrapper)
            }

            if (typeof option.tabContent == 'object') {
                tabContent = option.tabContent
            } else {
                tabContent = target.find(option.tabContent)
            }

            tabContent.click(function () {
                if (typeof option.content == 'object') {
                    content = option.content
                } else {
                    content = target.find(option.content)
                }
                var nowIndex = $(this).index();
                tabContent.removeClass(option.tabClass);
                $(this).addClass(option.tabClass);
                content.removeClass(option.contentClass);
                content.eq(nowIndex).addClass(option.contentClass);
                option.fn.call(this);
            })

        },
        placeHolder: {
            _check: function () {
                return 'placeholder' in document.createElement('input');
            },
            init: function (option) {
                if (!this._check()) {
                    this.fix(option);
                }
            },
            fix: function (option) {
                var target = '',
                    option = option || '';

                if(option.wrapper){
                    target = option.wrapper;
                }else{
                    target = jQuery(':input[placeholder]');
                }

                target.each(function (index, element) {
                    var self = $(this),
                        txt = self.attr('placeholder');
                    self.next("[rel='placeholder']").remove();
                    self.wrap($('<span></span>').css({
                        position: 'relative',
                        display: 'inline-block',
                        padding: 0,
                        zoom: '1',
                        border: 'none',
                        background: 'none',
                        margin: 0
                    }));

                    var pos = self.position(),
                        h = self.outerHeight(true),
                        paddingleft = self.css('padding-left'),
                        style = option.style || '';
                    var holder = $('<span rel="placeholder"></span>').text(txt).css({
                        position: 'absolute',
                        whiteSpace: 'nowrap',
                        left: style.left || 0,
                        top: 0,
                        height: style.height || '30px',
                        lineHeight: style.lineHeight || '30px',
                        paddingLeft: paddingleft,
                        color: '#aaa'
                    }).appendTo(self.parent());


                    if (self.val() != '') {
                        holder.hide();
                    }
                    self.focusin(function (e) {
                        holder.hide();
                    }).focusout(function (e) {
                        if (!self.val()) {
                            holder.show();
                        }
                    });
                    holder.click(function (e) {
                        e.stopPropagation();
                        holder.hide();
                        self.focus();
                        setTimeout(function () {
                            self.trigger('click');
                        }, 20)
                    });
                });
            },
            set: function(option){ // 设置
                if(!option){
                    return;
                }
                if(!this._check()){
                    var dPlaceholder = option.wrapper.siblings('i[rel=placeholder]');
                    if(dPlaceholder.length == 1){
                        dPlaceholder.html(option.content);
                    }
                }else{
                    option.wrapper.attr('placeholder', option.content);
                }
            }
        },
        allclick:function(options){
            this.allclick.def = {
                evObj:null,
                openClass:"cur",
                list:'li',
                fn:''
            };
            var opts = $.extend(this.allclick.def, options);
            var evObj = opts.evObj,openClass = opts.openClass,_fn = opts.fn;

            if(!typeof evObj == "object"){
                return false
            }
            evObj.each(function(){
                $(this).on('click',opts.list,function(){
                    if($(this).hasClass('disable')){
                        return;
                    }
                    $(this).addClass(openClass).siblings().removeClass(openClass);
                    _fn&&_fn.call($(this));
                })
            })
        },
        allWdatePicker: function(option){
            var option = $.extend(true, {
                op: {
                    maxDate:'%y-%M-{%d-1}',
                    isShowClear:false,
                    readOnly:true,
                    qsEnabled:false,
                    isShowToday: false,
                    isShowOK: false
                },
                type: 'click'
            }, option);

            option.op.onpicked = function(dp){
                return;
                option.onpicked.call(this, dp);
                if (!dp.el)return;
                $(dp.el).removeClass('open')
            }

            option.evObj.each(function(){
                $(this)[option.type](function(){
                    WdatePicker(option.op);
                    $('.Wdate').not($(this)).removeClass('open');
                })
            })
        },
        animate: function(evObj){
            evObj.each(function(){
                var width = $(this).attr('data-w');
                $(this).animate({
                    'width':width
                },1000);
            })
        },
        allSelect: function(option){
            var option = $.extend({
                evObj: '',
                fn: function(){

                },
                type: null,
                keydomFn: function(){

                },
                timeoutFlag: '',
                keydomDelay: 300
            }, option)

            option.evObj.find('ul').each(function(){
                var $this = $(this);
                ($this.children().length == 2) && $this.addClass('col2');
                ($this.children().length == 3) && $this.addClass('col3');
            })

            option.evObj.click(function(e){
                e.stopPropagation();
                $('.category').not($(this)).removeClass('on')
                var $this = $(this);
                $this.toggleClass('on');

                $(document).one('click', function () {
                    if ($this.hasClass('on')) {
                        $this.removeClass('on');
                    }
                    //if($('.tooltip').hasClass('show')){
                    //	$('.tooltip.show').removeClass('show');
                    //}
                })
            })

            option.evObj.on('click', 'li', function (e) {
                var $this = $(this),
                    dCategory = $this.closest('.category');
                $this.addClass('active').siblings().removeClass('active');

                if(option.type == 'input'){
                    dCategory.find('input').val($this.text());
                }else{
                    dCategory.find('span').text($this.text());
                }

                option.fn.call(this);
            });

            if(option.type == 'input'){
                option.evObj.on('keydown', 'input', function () {
                    var _this = this;
                    clearTimeout(option.timeoutFlag);
                    option.timeoutFlag = setTimeout(function(){
                        option.keydomFn.call(_this);
                        option.evObj.addClass('on');
                    }, option.keydomDelay)
                });
            }
        },
        showModalDialog: function(option){
            var option = $.extend({
                back: 'modal-back',
                wrap: null,
                duration: 1000
            },option);

            if(!option.wrap){
                return;
            }

            var dBack = $(option.back);
            if(dBack.length === 0){
                dBack = $('<div class="' + option.back + '"></div>');
                $('body').append(dBack);
            }

            var dWrap;
            if(typeof option.wrap == 'object'){
                dWrap = option.wrap;
            }else{
                dWrap = $(option.wrap)
            }

            dWrap.fadeIn(option.duration);
            dBack.fadeIn(option.duration);


            dWrap.find('.close').off().on('click', function(){
                closeDom([dWrap, dBack]);
            })
            dBack.off().on('click', function(){
                closeDom([dWrap, dBack]);
            })

            function closeDom(domArr){
                for(var i = 0, len = domArr.length ; i < len; i++){
                    $(domArr[i]).fadeOut(option.duration)
                }
            }
        },
        renderChannelChart: function(options) {
            this.renderChannelChart.def = {
                id: '',
                data: '',
                type: '',
                color: '',
                iconf: null,
                defsel: null,
                timeArr: '',
                curData: '',
                compareData: '',
                fn: '',
                formatter: function(){

                },
                title: '',
                axisLabelShow: true,
                legendData: [],
                legendshow: true
            };
            if (!Array.prototype.forEach) {
                Array.prototype.forEach = function(fun) {
                    var len = this.length;
                    if (typeof fun != "function")
                        throw new TypeError();

                    var thisp = arguments[1];
                    for (var i = 0; i < len; i++) {
                        if (i in this)
                            fun.call(thisp, this[i], i, this);
                    }
                };
            }
            var opts = this.opts = $.extend(this.renderChannelChart.def, options);
            var item = $("#" + opts.id);
            if (item.length <= 0) {
                return false;
            }
            var mainChart = echarts.init(item[0]);
            var _color = opts.color,
                _data = opts.data,
                legarr = [],
                _fn = opts.fn,
                timeArr = opts.timeArr,
                curData = opts.curData,
                compareData = opts.compareData;
                title = opts.title;
                axisLabelShow = opts.axisLabelShow;

            var legend =  { //右上角标记
                data: ['本期', '对比期'],
                itemWidth: 10
            };

            opts.legendBottom ? legend.bottom = opts.legendBottom : '';
            opts.legendTop ? legend.top = opts.legendTop : '';
            opts.legendRight ? legend.right = opts.legendRight : legend.right = '2%';
            opts.legendLeft ? (legend.left = opts.legendLeft, legend.right = 'auto') : '';
            opts.legendshow ? (legend.show = true) : (legend.show = false);

            var settings = (function(type) {
                if (type == "line") {
                    return {
                        color: ['#ade653', '#51ddf4'],
                        tooltip: {
                            trigger: 'axis',
                            backgroundColor: 'rgba(255, 255, 255, 1)',
                            borderColor: '#b4aead',
                            borderWidth: 1,
                            color: '#e1e1e1',
                            axisPointer: {
                                lineStyle: {
                                    width: 1,
                                    color: '#ff9900'
                                }
                            },
                            formatter: function(params) {
                                return opts.formatter(params);
                            },
                            textStyle: {
                                color: '#000'
                            }
                        },
                        title: {
                            show: true,
                            text: title
                        },
                        grid: {
                            containLabel: true,
                            left: '2%',
                            right: '6%',
                            bottom: '3%',
                        },
                        legend: legend,
                        xAxis: [{
                            type: 'category',
                            boundaryGap: false,
                            data: timeArr,
                            max: 'dataMax',
                            splitLine: { //分割线
                                show: false
                            },
                            axisLabel:{
                                show: axisLabelShow
                            },
                            scala: true
                        }],
                        yAxis: [{
                            type: 'value',
                            splitNumber: 4 //Y轴分割段数
                        }],
                        series: [{
                            name: '本期',
                            type: 'line',
                            smooth: true,
                            zlevel: 1,
                            showSymbol:false,
                            egendHoverLink:false,
                            hoverAnimation:false,
                            clipOverflow: true,
                            symbol:'circle',
                            symbolSize: 10,
                            //markLine:{
                            //    label:{
                            //        normal:{show: true}
                            //    }
                            //},
                            itemStyle: {
                                normal: {
                                    areaStyle: {
                                        type: 'default',
                                        color: '#ade653',
                                        opacity: 0.8
                                    }
                                }

                            },
                            data: curData

                        }, {
                            name: '对比期',
                            type: 'line',
                            smooth: true,
                            showSymbol:false,
                            egendHoverLink:false,
                            hoverAnimation:false,
                            clipOverflow: false,
                            //symbol: 'circle',//线上的锚点
                            symbol:'circle',
                            symbolSize: 10,
                            itemStyle: {
                                normal: {
                                    areaStyle: {
                                        type: 'default',
                                        color: '#51ddf4',
                                        opacity: 1
                                    }
                                }
                            },
                            data: compareData
                        }]

                    }
                } else if (type == "pie") {
                    _data.forEach(function(o, i) {
                        if (opts.iconf) {
                            legarr = [].concat.call(legarr, {
                                icon: 'image://' + opts.iconf + i + '.png',
                                name: o.name
                            });
                        } else {
                            legarr = [].concat.call(legarr, {
                                icon: 'circle',
                                name: o.name
                            });
                        }
                    });
                    return {
                        color: _color,
                        tooltip : {
                            trigger: 'item',
                            formatter: "{a} <br/>{b} : {c} ({d}%)"
                        },
                        grid: {
                            containLabel: true
                        },
                        series : [
                            {
                                name: '访问来源',
                                type: 'pie',
                                radius : '84%',
                                center: ['50%', '50%'],
                                label:{
                                    normal:{
                                        textStyle:{
                                            color:'#323232'
                                        }
                                    }
                                },
                                data: _data
                            }
                        ],
                        legend: opts.legendData
                    }
                }
            })(opts.type);
            mainChart.setOption(settings);
            _fn && _fn.call(this, mainChart, item);
        },

    }

    common.slideFn = function(op){
        this.option = $.extend({
            wrap: null,
            prevBtn: '.prev',
            nextBtn: '.next',
            slideWrap: 'ul',
            slideItem: 'li',
            slideWidth: null,
            showNum: null,
            finishFn: function(){

            },
            initFn: function(){

            }
        }, op);
        this.wrap = null;
        this.slideWrap = null;
        this.slideWidth = 0;
        this.index = 0;
        this.max = 0;
        this.animating = 0;
        this.showNum = 0;
        this._init();

    }

    common.slideFn.prototype = {
        _init: function(){
            var _this = this,
                option = _this.option,
                slideItem;
            _this.wrap = $(option.wrap);

            _this.slideWrap = _this.wrap.find(option.slideWrap);
            slideItem = _this.slideWrap.find(option.slideItem);
            _this.slideWidth = option.slideWidth ? option.slideWidth : slideItem.eq(0).width();
            _this.showNum = option.showNum ? option.showNum : (_this.wrap.width() / _this.slideWidth);
            _this.max = slideItem.length;

            _this.wrap.find(option.nextBtn).click(function(){
                _this._go(_this.index + 1)
            })

            _this.wrap.find(option.prevBtn).click(function(){
                _this._go(_this.index - 1)
            })

            _this.option.initFn.call(_this);
        },
        _go: function(index){
            var _this = this;
            if(index > _this.max - _this.showNum  || index < 0 || _this.animating == 1){
                return;
            }
            _this.animating = 1;
            _this.index = index
            _this.slideWrap.animate({
                'marginLeft': -_this.slideWidth * index
            }, function(){
                _this.option.finishFn.call(_this);
                _this.animating = 0;
            })
        }
    }


})();

(function () {
    //弹出框组件

    /**
     * alert
     */
    common.alert = function (opts) {
        var alert = new common.Alert(opts);
        alert.show();
    };
    common.Alert = function (opts) {
        // 初始化属性
        this.setProps(opts);
        // 设置DOM
        this.setDom();
        // 设置内容
        this.setContent();
    };
    common.Alert.prototype.setProps = function (opts) {
        // 初始化属性
        this.props = {};
        opts = opts || {};
        this.props.dropback = opts.dropback || true;
        this.props.content = opts.content || '发生错误';
        this.props.hasDel = opts.hasDel || true;
        this.props.wrapClass = opts.wrapClass || 'sn-alert-wrapper';
        this.props.backClass = opts.backClass || 'sn-alert-back';
    };
    common.Alert.prototype.setDom = function () {
        // 设置dom 并用对应属性做存储
        var queryDom, queryDomBack;
        queryDom = document.querySelectorAll('.' + this.props.wrapClass);
        if (queryDom.length === 0) {
            queryDom = document.createElement('div');
            queryDom.className = this.props.wrapClass;
            queryDom.innerHTML = ['<div class="header clearfix">', '<div class="title">温馨提示</div> <div class="del"></div>', '</div>', '<div class="content">', '<div class="tipContent"></div>', '</div>', '<div class="btn">确定</div>'].join('');
            document.body.appendChild(queryDom);
        }
        this.dom = queryDom[0] || queryDom;

        queryDomBack = document.querySelectorAll('.' + this.props.backClass);
        if (queryDomBack.length === 0) {
            queryDomBack = document.createElement('div');
            queryDomBack.className = this.props.backClass;
            document.body.appendChild(queryDomBack);
        }
        this.dropBack = queryDomBack[0] || queryDomBack;

        // 设置是否展示 x
        if (!this.props.hasDel) {
            queryDom.querySelector('.del').style.display = 'none';
        }
    };
    common.Alert.prototype.setContent = function () {
        this.dom.querySelector('.tipContent').innerHTML = this.props.content;
    };
    common.Alert.prototype.bindEvent = function () {
        // 全量绑定事件
        var _this = this;
        var Del = _this.dom.querySelector('.del');
        var Btn = _this.dom.querySelector('.btn');
        var DropBack = _this.dropBack;
        // 用这个变量来存储触发destory事件
        _this.destoryFunc = _this.destory.bind(_this);

        Del.addEventListener('click', _this.destoryFunc, false);
        Btn.addEventListener('click', _this.destoryFunc, false);
        if (_this.props.dropback) {
            DropBack.addEventListener('click', _this.destoryFunc, false);
        }
    };
    common.Alert.prototype.show = function () {
        this.bindEvent();
        this.dropBack.style.display = 'block';
        this.dom.style.display = 'block';
    };
    common.Alert.prototype.destory = function () {
        var _this = this;
        _this.dom.style.display = 'none';
        _this.dropBack.style.display = 'none';
        var Del = _this.dom.querySelector('.del');
        var Btn = _this.dom.querySelector('.btn');
        var DropBack = _this.dropBack;
        Del.removeEventListener('click', _this.destoryFunc, false);
        Btn.removeEventListener('click', _this.destoryFunc, false);
        if (_this.props.dropback) {
            DropBack.removeEventListener('click', _this.destoryFunc, false);
        }
        _this = null;
    };

})();


$(function () {
    common.eventsInit();
    common.topMenuInit();
    common.leftMenuInit();
    //common.allTabClickInit();
})