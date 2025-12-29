/**
 * Created by 14100377 on 2016/2/26.
 */
window.YY = window.YY || {};

YY.renderChannelChartinit = 0;

//大区、品类数据
YY.renderPercentTable = function(el,dataObj){
    var $el = $(el);
    if(!$el.length){
        return
    }

    //预处理数据 进度条最宽400px
    var items = dataObj.data,
        MAX_W = 300,
        MIN_W = 10,
        MAX_COUNT = items[0].count;

    $.each(items,function(index,item){
        var scale = item.count / MAX_COUNT;
        item.widths = [];
        item.count = common.addKannma(item.count);
        $.each(item.detail,function(i,percent){
            var w = (percent * MAX_W * scale).toFixed(1);
            item.widths.push((w<MIN_W)?MIN_W:w);
            item.detail[i] = (parseFloat(percent) * 100).toFixed(1) + '%';
        })
    })

    var $temp = $.templates('#percentTableTemp');
    $el.html($temp.render(dataObj));
}

$(function(){
    // 实时概况
    common.renderChannelChart({
        id: 'mainChart',
        timeArr: ["00:00", "01:00", "02:00", "03:00", "04:00", "05:00", "06:00", "07:00", "08:00", "09:00", "10:00", "11:00", "12:00", "13:00", "14:00", "15:00", "16:00", "17:00", "18:00", "19:00", "20:00", "21:00", "22:00", "23:00"],
        curData: [2571296, 1642076, 1253549, 1103772, 1154933, 975191, 2008227, 3952152, 2779092, 5015732, 5957072, 5541254, 5197585, 5319141, 5447887, 5590428, 5580906, 4903339, 4381367, 125285],
        compareData: [1887250, 1152593, 869623, 742374, 676378, 727811, 1470415, 2230158, 3077535, 4055910, 4804120, 4524081, 4126187, 4240524, 4315941, 4435622, 4396123, 3856298, 3475809, 3888193, 4510846, 4622470, 4222968, 2875199],
        type: 'line',
        formatter: function (params) {
            var str1, str2;
            if (params.length == 2) {
                if (params[0].dataIndex == params[1].dataIndex) {
                    str1 = '<br><span style="color:#ade653">' + params[0].seriesName + ':  </span>' + (common.addKannma(params[0].value) + "").replace(undefined, 0);
                    str2 = '<br><span style="color:#51def4">' + params[1].seriesName + ':  </span>' + (common.addKannma(params[1].value) + "").replace(undefined, 0);
                    return params[1].name + ' - ' + params[1].name.split(":")[0] + ':59' + str1 + str2;
                } else if (params[0].dataIndex < params[1].dataIndex) {
                    str1 = '<br><span style="color:#ade653">' + params[1].seriesName + ':  </span>' + (common.addKannma(params[1].value) + "").replace(undefined, 0);
                    return params[1].name + ' - ' + params[1].name.split(":")[0] + ':59' + str1;
                } else if (params[0].dataIndex > params[1].dataIndex) {
                    str1 = '<br><span style="color:#ade653">' + params[0].seriesName + ':  </span>' + (common.addKannma(params[0].value) + "").replace(undefined, 0);
                    return params[0].name + ' - ' + params[0].name.split(":")[0] + ':59' + str1;
                }
            } else if (params.length == 1) {
                if (params[0].seriesName == '本期') {
                    str1 = '<br><span style="color:#ade653">' + params[0].seriesName + ':  </span>' + (common.addKannma(params[0].value) + "").replace(undefined, 0);
                } else if (params[0].seriesName == '对比期') {
                    str1 = '<br><span style="color:#51def4">' + params[0].seriesName + ':  </span>' + (common.addKannma(params[0].value) + "").replace(undefined, 0);
                }
                return params[0].name + ' - ' + params[0].name.split(":")[0] + ':59' + str1;
            }
        },
        fn: function(){
            var renderChartInfo = function(op){
                var $wrapper = $('#mainChartInfo-wrapper'),
                    $curCount = $wrapper.children('.curData'),
                    $trend = $wrapper.find('.trend'),
                    $rate = $trend.children('span'),
                    $compareWrapper = $wrapper.children('.compareData'),
                    $time = $compareWrapper.find('.time > span'),
                    $tip = $compareWrapper.find('.timeTooltip'),
                    $compareData = $compareWrapper.find('.data > span');

                if($wrapper.length === 0){
                    return
                }

                $trend[0].className = 'trend';

                var rate = (op.compareCount == 0) ? '--' :(op.curCount - op.compareCount)/ op.compareCount * 100;
                $trend.addClass((op.curCount - op.compareCount)>0 ? 'up' : (op.curCount === op.compareCount) ? 'eq' : 'down');

                $time.html(op.time);
                $tip.html(op.tip);

                common.countUp($curCount[0], 0, op.curCount);
                common.countUp($compareData[0], 0, op.compareCount);

                if(op.curCount !== op.compareCount){
                    common.countUp($rate[0], 0, rate);
                }
                else{
                    $rate.html('--')
                }
            };

            renderChartInfo({
                curCount: 69966355,
                compareCount: 55064752,
                time: '2016/02/25/ 19:00:00',
                tip: '对比期时间'
            });
        }
    });

    //品类表格数据
    YY.renderPercentTable(
        '#cateTable',
        {
            data: [
                {
                    "name":"通讯",
                    "percent":"97.1",
                    "count":"2123435",
                    "detail":["0.333","0.334","0.333"]
                },
                {
                    "name":"数码",
                    "percent":"87.1",
                    "count":"1723435",
                    "detail":["0.233","0.234","0.533"]
                },
                {
                    "name":"母婴",
                    "percent":"87.1",
                    "count":"1323435",
                    "detail":["0.333","0.034","0.633"]
                }
            ]
        }
    );

    //大区表格数据
    YY.renderPercentTable(
        '#areaTable',
        {
            data: [
                {
                    "name":"南京",
                    "percent":"97.1",
                    "count":"2123435",
                    "detail":["0.333","0.334","0.333"]
                },
                {
                    "name":"北京",
                    "percent":"87.1",
                    "count":"1723435",
                    "detail":["0.233","0.234","0.533"]
                },
                {
                    "name":"上海",
                    "percent":"87.1",
                    "count":"1323435",
                    "detail":["0.333","0.034","0.633"]
                }
            ]
        }
    );

    // 渠道分析
    common.renderChannelChart({
        id: 'channelChart',
        type: 'pie',
        data: [
            {
                "value":"4499999",
                "name":"门店",
                "compare":"96.82",
                "nodes":[
                    [{"value":"1499999","name":"POS购线上"},{"value":"30000000","name":"POS购门店"}]
                ]
            },
            {
                "value":"3499999",
                "name":"易购",
                "compare":"66.82",
                "nodes":[
                    [{"value":"1499999","name":"C店"},{"value":"10000000","name":"自营"},{"value":"10000000","name":"其他"}],
                    [{"value":"1499999","name":"WAP"},{"value":"10000000","name":"APP"},{"value":"10000000","name":"PC"}]
                ]
            },
            {
                "value":"2499999",
                "name":"天猫",
                "compare":"26.82",
                "nodes":[
                    [{"value":"1499999","name":"WAP"},{"value":"10000000","name":"APP"},{"value":"10000000","name":"PC"}]
                ]
            }
        ],
        color: ['#ffb739', '#bdeb75','#51ddf4'],
        fn: function(){
            //右侧进度条
            var renderChartInfo = function(data){
                var datailData = [];
                var $wrapper = $('#channelChartInfo-wrapper');

                var total = 0,
                    maxValue = 0;
                $.each(data,function(index,item){
                    if(item.value > maxValue){
                        maxValue = item.value;
                    }
                    total += parseInt(item.value);
                    item.valueStr = common.addKannma(item.value);
                    $.each(item.nodes,function(index,item){
                        datailData.push(item);
                    })
                })
                $.each(data,function(index,item){
                    item.percent = (Math.abs(item.compare) / item.value - item.compare).toFixed(2).toString().replace("NaN","--");
                    item.progressWidth = (item.value / maxValue) * 100
                    if(item.progressWidth < 1){
                        item.progressWidt = 1;
                    }
                })

                var $temp = $.templates('#channelDetailChart');
                $wrapper.html($temp.render({"data":data}));

                if(YY.renderChannelChartinit == 1){
                    return
                }
                YY.renderChannelChartinit = 1;

                $wrapper.on('click','.detail',function(){
                    var $this = $(this);
                    $this.toggleClass('open');
                    $('.chart-td').eq($this.data('index')).toggleClass('hide');
                });

            };

            renderChartInfo([
                {
                    "value":"4499999",
                    "name":"门店",
                    "compare":"96.82",
                    "nodes":[
                        [{"value":"1499999","name":"POS购线上"},{"value":"30000000","name":"POS购门店"}]
                    ]
                },
                {
                    "value":"3499999",
                    "name":"易购",
                    "compare":"66.82",
                    "nodes":[
                        [{"value":"1499999","name":"C店"},{"value":"10000000","name":"自营"},{"value":"10000000","name":"其他"}],
                        [{"value":"1499999","name":"WAP"},{"value":"10000000","name":"APP"},{"value":"10000000","name":"PC"}]
                    ]
                },
                {
                    "value":"2499999",
                    "name":"天猫",
                    "compare":"26.82",
                    "nodes":[
                        [{"value":"1499999","name":"WAP"},{"value":"10000000","name":"APP"},{"value":"10000000","name":"PC"}]
                    ]
                }
            ]);
        }
    })



    //渠道分析进度条动画效果
    common.animate($('.channel .progress em'));


    var dHeaderView = $('.header-view'),
        dDateSelector = dHeaderView.find('.date-selector');

    // 日历绑定
    common.allWdatePicker({
        evObj: dDateSelector.eq(0),
        op: {
            //maxDate: '2099-12-31', // 最大的日期
        },
        onpicked: function(dp){ // 选择完日期回调
            //this: 指向文本框
            //dp: 指向$dp
            //dp.cal: 指向日期控件对象
            console.log(this)
            console.log(dp)
        }
    })
    common.allWdatePicker({
        evObj: dDateSelector.eq(1),
        op: {
            //maxDate: '2099-12-31', // 最大的日期
        },
        onpicked: function(dp){ // 选择完日期回调
            //this: 指向文本框
            //dp: 指向$dp
            //dp.cal: 指向日期控件对象
            console.log(this)
            console.log(dp)
        }
    })


})

