$(function(){

	//btnGroup start
	$('.btnGroup .btnItem').on('click', function(){
		if($(this).hasClass('active')) return;

		$(this).addClass('active').siblings().removeClass('active');
		$(this).parent().attr('data-check', $(this).text())
	})
	//btnGroup end

	//搜索条件  start
	var config_locale = {
		"format": 'YYYY/MM/DD',
		"applyLabel": "确定",
		"cancelLabel": "取消",
		"fromLabel": "起始时间",
		"toLabel": "结束时间'",
		"customRangeLabel": "自定义",
		"today": "今日",
		"daysOfWeek": ["日", "一", "二", "三", "四", "五", "六"],
		"monthNames": ["一月", "二月", "三月", "四月", "五月", "六月", "七月", "八月", "九月", "十月", "十一月", "十二月"]
	};

	//checkbox-ago
	$('#checkbox-ago').on('change', function(){
		if(this.checked){
			$('#time-during-ago').removeAttr('disabled').removeClass('disabled')
		}else{
			$('#time-during-ago').attr('disabled', true).addClass('disabled')
		}
	})

	$('.search .btn-search').on('click', function(){
		var config 			  = {};
		config.timeNow 		  = $('#time-during-now').val();
		config.timeAgo 		  = $('#time-during-ago').val();
		config.channel 		  = $('.search .btnGroup').attr('data-check');
		config.type 		  = $('#select-quan').val();
		config.method 		  = $('#select-method').val();
		config.checkbox_duibi = $('#checkbox-ago')[0].checked;

		console.log(config)

		if(config.checkbox_duibi){
			$('#total-now').hide();
			$('#total-contrast').show();
		}else{
			$('#total-contrast').hide();
			$('#total-now').show();
		}

		// loading
		$('.pop, .pop .confirm-loading').show();
		//下面setTimout是模拟ajax，开发删除
		setTimeout(function(){
			$('.pop, .pop .confirm-loading').hide();
		}, 2000);
	})
	//搜索条件 end

	//发券渠道分布 start
	$('.entrance .btnGroup .btnItem').on('click', function(){
		var name_check = $(this).text();
		console.log(name_check)
	})
	//发券渠道分布 start


	//发券用券趋势 start
	initChart(option_trend, 'echart-trend')
	//发券用券趋势 end
	
	//发券用券会员构成 start
	initChart(option_member, 'echarts-member')
	//发券用券会员构成 end

	//help
	$('.help .icon-question-circle').on('click', function(){
		$(this).next().toggle();
	})
	$('.help .btn-iknowe').on('click', function(){
		$(this).parents('.con').hide();
	})
})